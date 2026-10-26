package main

import (
	"encoding/xml"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

type xmlCatalogItem struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	Price float64 `json:"price"`
	Cost float64 `json:"cost"`
	Barcode string `json:"barcode"`
	Category string `json:"category"`
	NCM string `json:"ncm"`
	CEST string `json:"cest"`
	CFOP string `json:"cfop"`
	TaxOrigin string `json:"tax_origin"`
	ImageURL string `json:"image_url"`
}

type xmlCatalogNode struct {
	XMLName xml.Name
	Text string `xml:",chardata"`
	Children []xmlCatalogNode `xml:",any"`
}

func normalizeXMLKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s { if unicode.IsLetter(r) || unicode.IsDigit(r) { b.WriteRune(r) } }
	return b.String()
}
func xmlChildValues(n xmlCatalogNode) map[string]string {
	m := map[string]string{}
	for _, c := range n.Children { key:=normalizeXMLKey(c.XMLName.Local); if key!="" { m[key]=strings.TrimSpace(c.Text) } }
	return m
}
func firstXMLValue(m map[string]string, keys ...string) string {
	for _, k := range keys { if v:=strings.TrimSpace(m[normalizeXMLKey(k)]); v!="" { return v } }
	return ""
}
func xmlNumber(raw string) (float64, bool) {
	raw=strings.TrimSpace(raw); if raw=="" { return 0,false }
	raw=strings.ReplaceAll(raw,"R$",""); raw=strings.ReplaceAll(raw," ","")
	if strings.Contains(raw,",") { raw=strings.ReplaceAll(raw,".",""); raw=strings.ReplaceAll(raw,",",".") }
	v,err:=strconv.ParseFloat(raw,64); return v,err==nil && !math.IsNaN(v) && !math.IsInf(v,0) && v>=0
}
func parseXMLCatalog(raw []byte) ([]xmlCatalogItem,error) {
	if len(raw)==0 { return nil,errors.New("O arquivo XML está vazio.") }
	if len(raw)>5<<20 { return nil,errors.New("O XML excede o limite de 5 MB.") }
	var root xmlCatalogNode
	dec:=xml.NewDecoder(strings.NewReader(string(raw))); dec.Strict=true
	if err:=dec.Decode(&root); err!=nil { return nil,fmt.Errorf("XML inválido: %w",err) }
	var out []xmlCatalogItem
	seen:=map[string]bool{}
	var walk func(xmlCatalogNode)
	walk=func(n xmlCatalogNode) {
		m:=xmlChildValues(n)
		name:=firstXMLValue(m,"nome","name","xProd","descricao_produto","product_name","produto_nome")
		priceRaw:=firstXMLValue(m,"preco_venda","preco","price","valor_venda","vProd","retail_price","selling_price")
		price,priceOK:=xmlNumber(priceRaw)
		if qRaw:=firstXMLValue(m,"qCom","quantidade_comercial","quantidade"); qRaw!="" && firstXMLValue(m,"vProd")!="" {
			if q,ok:=xmlNumber(qRaw);ok && q>0 { price=price/q; priceOK=true }
		}
		if name!="" && priceOK {
			cost,_:=xmlNumber(firstXMLValue(m,"preco_custo","custo","cost","valor_custo","purchase_price"))
			item:=xmlCatalogItem{
				Code:firstXMLValue(m,"codigo","code","sku","cProd","codigo_interno"),
				Name:name, Unit:firstXMLValue(m,"unidade","unit","uCom","un"),
				Price:math.Round(price*100)/100, Cost:math.Round(cost*100)/100,
				Barcode:firstXMLValue(m,"codigo_barras","barcode","ean","gtin","cEAN","cEANTrib"),
				Category:firstXMLValue(m,"categoria","category","grupo","category_name"),
				NCM:firstXMLValue(m,"ncm"), CEST:firstXMLValue(m,"cest"), CFOP:firstXMLValue(m,"cfop"),
				TaxOrigin:firstXMLValue(m,"origem_tributaria","tax_origin","orig"),
				ImageURL:firstXMLValue(m,"imagem_url","image_url","imagem","image"),
			}
			key:=strings.ToLower(item.Name)+"|"+item.Barcode+"|"+item.Code
			if !seen[key] { seen[key]=true; out=append(out,item) }
		}
		for _,c:=range n.Children { walk(c) }
	}
	walk(root)
	if len(out)==0 { return nil,errors.New("Não encontrei produtos no XML. Use os campos nome e preco_venda (ou name e price) em cada elemento produto/item. O XML de exemplo está disponível na tela de cadastro.") }
	if len(out)>5000 { return nil,errors.New("O XML contém mais de 5.000 produtos por arquivo.") }
	return out,nil
}

func (a *App) xmlImportPreview(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost { w.Header().Set("Allow","POST"); http.Error(w,"Método não permitido",405); return }
	r.Body=http.MaxBytesReader(w,r.Body,5<<20)
	raw,err:=io.ReadAll(r.Body); if err!=nil { http.Error(w,"Não foi possível ler o XML (limite de 5 MB).",400); return }
	items,err:=parseXMLCatalog(raw); if err!=nil { http.Error(w,err.Error(),422); return }
	jsonOut(w,200,map[string]any{"source":"XML","count":len(items),"items":items})
}
func (a *App) xmlImportApply(w http.ResponseWriter,r *http.Request) {
	if r.Method!=http.MethodPost { w.Header().Set("Allow","POST"); http.Error(w,"Método não permitido",405); return }
	user:=a.currentUser(r)
	if user==nil || (user.Role!="admin" && user.Role!="manager") { http.Error(w,"Somente administrador ou gerente pode importar produtos.",403); return }
	var body struct { Items []xmlCatalogItem `json:"items"` }
	if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,3<<20)).Decode(&body);err!=nil { http.Error(w,"Prévia XML inválida.",400); return }
	if len(body.Items)==0 || len(body.Items)>1000 { http.Error(w,"Selecione entre 1 e 1.000 produtos por importação.",400); return }
	tx,err:=a.db.Begin(); if err!=nil { http.Error(w,"Não foi possível iniciar a importação.",500); return }; defer tx.Rollback()
	imported,skipped:=0,0
	for _,p:=range body.Items {
		p.Name=strings.TrimSpace(p.Name); p.Code=strings.TrimSpace(p.Code); p.Barcode=strings.TrimSpace(p.Barcode); p.Category=strings.TrimSpace(p.Category); p.Unit=strings.TrimSpace(p.Unit)
		if p.Name=="" || len(p.Name)>200 || p.Price<0 || p.Price>1e9 || p.Cost<0 || p.Cost>1e9 { http.Error(w,"Produto com nome ou preço inválido; nenhum item foi gravado.",400); return }
		var exists int
		if p.Barcode!="" { err=tx.QueryRow("SELECT COUNT(*) FROM products WHERE barcode=? AND barcode<>''",p.Barcode).Scan(&exists) } else if p.Code!="" { err=tx.QueryRow("SELECT COUNT(*) FROM products WHERE code=?",p.Code).Scan(&exists) } else { err=tx.QueryRow("SELECT COUNT(*) FROM products WHERE name=? COLLATE NOCASE",p.Name).Scan(&exists) }
		if err!=nil { http.Error(w,"Erro ao verificar produtos duplicados.",500); return }; if exists>0 { skipped++; continue }
		code:=p.Code
		if code=="" { code=fmt.Sprintf("XML-%06d",imported+1) }
		var count int
		if err=tx.QueryRow("SELECT COUNT(*) FROM products WHERE code=?",code).Scan(&count);err!=nil { http.Error(w,"Erro ao validar código interno.",500);return }
		if count>0 { skipped++;continue }
		if p.Unit=="" { p.Unit="UN" }; if len(p.Unit)>10 { p.Unit=p.Unit[:10] }
		var categoryID int64
		if p.Category!="" {
			if len(p.Category)>100 { p.Category=p.Category[:100] }
			err=tx.QueryRow("SELECT id FROM categories WHERE name=? COLLATE NOCASE",p.Category).Scan(&categoryID)
			if err!=nil { res,e:=tx.Exec("INSERT INTO categories(name) VALUES(?)",p.Category); if e!=nil { http.Error(w,"Não foi possível criar categoria.",500);return }; categoryID,_=res.LastInsertId() }
		}
		imageURL:=strings.TrimSpace(p.ImageURL)
		if imageURL!="" { u,e:=url.Parse(imageURL); if e!=nil || (u.Scheme!="https"&&u.Scheme!="http") || u.Host=="" { imageURL="" } }
		_,err=tx.Exec("INSERT INTO products(code,name,unit,price_cents,cost_cents,barcode,category_id,stock,ncm,cest,cfop,tax_origin,active,image_url) VALUES(?,?,?,?,?,?,NULLIF(?,0),0,?,?,?,?,1,?)",
			code,p.Name,p.Unit,int64(math.Round(p.Price*100)),int64(math.Round(p.Cost*100)),p.Barcode,categoryID,p.NCM,p.CEST,p.CFOP,p.TaxOrigin,imageURL)
		if err!=nil { http.Error(w,"Não foi possível gravar produto; a importação foi cancelada.",409);return }; imported++
	}
	if _,err=tx.Exec("INSERT INTO audit_log(user_id,action,entity_type,entity_id,details) VALUES(?,?,?,?,?)",user.ID,"xml_catalog_import","product","",fmt.Sprintf("Importação XML: %d inseridos, %d ignorados por duplicidade",imported,skipped));err!=nil { http.Error(w,"Erro ao registrar auditoria; importação cancelada.",500);return }
	if err=tx.Commit();err!=nil { http.Error(w,"Não foi possível confirmar a importação.",500);return }
	jsonOut(w,200,map[string]any{"imported":imported,"skipped":skipped})
}
