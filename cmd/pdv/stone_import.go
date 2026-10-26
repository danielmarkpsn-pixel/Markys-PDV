package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type stoneCatalogItem struct {
	Name string `json:"name"`
	Price float64 `json:"price"`
	Barcode string `json:"barcode"`
	Category string `json:"category"`
	ImageURL string `json:"image_url"`
}

var (
	stoneJSONLD = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	stoneJSON = regexp.MustCompile(`(?is)<script[^>]*type=["']application/json["'][^>]*>(.*?)</script>`)
	stoneHTMLTags = regexp.MustCompile(`(?is)<[^>]+>`)
	stoneSpace = regexp.MustCompile(`\s+`)
)

func validateStoneCatalogURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "catalogo.stone.com.br") || u.User != nil {
		return nil, errors.New("Informe o link público HTTPS de catalogo.stone.com.br.")
	}
	return u, nil
}

func fetchStoneCatalog(raw string) ([]byte, error) {
	u, err := validateStoneCatalogURL(raw)
	if err != nil { return nil, err }
	client := &http.Client{
		Timeout: 12*time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || !strings.EqualFold(req.URL.Hostname(), "catalogo.stone.com.br") || req.URL.Scheme != "https" {
				return errors.New("O catálogo tentou redirecionar para um endereço não permitido.")
			}
			return nil
		},
	}
	// Resolve only the Stone hostname; do not follow arbitrary URLs supplied by the user.
	if net.ParseIP(u.Hostname()) != nil { return nil, errors.New("Endereço inválido.") }
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "MARKYS-PDV-Catalog-Importer/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil { return nil, fmt.Errorf("Não foi possível acessar o catálogo público: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, fmt.Errorf("A Stone respondeu com HTTP %d.", resp.StatusCode) }
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20+1))
	if err != nil { return nil, errors.New("Não foi possível ler a página do catálogo.") }
	if len(data) > 5<<20 { return nil, errors.New("A página do catálogo excedeu o limite de 5 MB.") }
	return data, nil
}

func parseStoneCatalog(html []byte) []stoneCatalogItem {
	out := []stoneCatalogItem{}
	seen := map[string]bool{}
	collect := func(raw []byte) {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var value any
		if dec.Decode(&value) == nil { walkStoneJSON(value, &out, seen) }
	}
	for _, match := range stoneJSONLD.FindAllSubmatch(html, -1) { collect(bytes.TrimSpace(match[1])) }
	for _, match := range stoneJSON.FindAllSubmatch(html, -1) { collect(bytes.TrimSpace(match[1])) }
	// Some storefronts embed a serialized product payload without JSON-LD. Try parsing
	// script bodies that look like JSON, while still accepting only records with product fields.
	scripts := regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)
	for _, match := range scripts.FindAllSubmatch(html, 100) {
		body := bytes.TrimSpace(match[1])
		if len(body) > 0 && (body[0] == '{' || body[0] == '[') && len(body) < 2<<20 { collect(body) }
	}
	return out
}

func walkStoneJSON(v any, out *[]stoneCatalogItem, seen map[string]bool) {
	switch x := v.(type) {
	case []any:
		for _, child := range x { walkStoneJSON(child, out, seen) }
	case map[string]any:
		name := stoneString(x["name"])
		if name == "" { name = stoneString(x["productName"]) }
		price := stonePrice(x)
		image := stoneImage(x["image"])
		if image == "" { image = stoneImage(x["imageUrl"]) }
		if name != "" && price >= 0 && (x["@type"] != nil || x["offers"] != nil || x["price"] != nil || x["productName"] != nil) {
			barcode := firstStoneString(x, "gtin13", "gtin", "gtin8", "gtin12", "sku", "barcode", "ean")
			category := firstStoneString(x, "category", "categoryName")
			key := strings.ToLower(strings.TrimSpace(name)) + "|" + strings.TrimSpace(barcode)
			if !seen[key] {
				seen[key] = true
				*out = append(*out, stoneCatalogItem{Name: strings.TrimSpace(name), Price: price, Barcode: barcode, Category: category, ImageURL: image})
			}
		}
		for _, child := range x { walkStoneJSON(child, out, seen) }
	}
}

func stoneString(v any) string {
	switch x := v.(type) {
	case string: return strings.TrimSpace(x)
	case json.Number: return x.String()
	case float64: return strconv.FormatFloat(x, 'f', -1, 64)
	default: return ""
	}
}
func firstStoneString(m map[string]any, keys ...string) string {
	for _, k := range keys { if s := stoneString(m[k]); s != "" { return s } }
	return ""
}
func stoneImage(v any) string {
	switch x := v.(type) {
	case string: return strings.TrimSpace(x)
	case []any:
		for _, item := range x { if s := stoneImage(item); s != "" { return s } }
	case map[string]any:
		return firstStoneString(x, "url", "contentUrl")
	}
	return ""
}
func stonePrice(m map[string]any) float64 {
	if offers, ok := m["offers"]; ok {
		switch x := offers.(type) {
		case []any:
			for _, item := range x { if mm, ok := item.(map[string]any); ok { if p := stonePrice(mm); p >= 0 { return p } } }
		case map[string]any:
			if p := stonePrice(x); p >= 0 { return p }
		}
	}
	for _, key := range []string{"price", "lowPrice", "priceAmount"} {
		if raw := stoneString(m[key]); raw != "" {
			raw = strings.ReplaceAll(strings.TrimSpace(raw), ",", ".")
			if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 { return n }
		}
	}
	return -1
}

func (a *App) stoneImportPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.Header().Set("Allow","POST"); http.Error(w,"Método não permitido",405); return }
	var body struct { URL string `json:"url"` }
	if err := json.NewDecoder(http.MaxBytesReader(w,r.Body,16<<10)).Decode(&body); err != nil { http.Error(w,"Informe o link do catálogo.",400); return }
	html, err := fetchStoneCatalog(body.URL)
	if err != nil { http.Error(w,err.Error(),400); return }
	items := parseStoneCatalog(html)
	if len(items) == 0 {
		http.Error(w,"A página pública não expôs os produtos em dados estruturados que o importador reconheça. Não importamos nomes ou preços estimados. Tente exportar o CSV pela Conta Stone ou envie uma cópia HTML da página para adaptar o leitor.",422)
		return
	}
	jsonOut(w,200,map[string]any{"source":"Stone","count":len(items),"items":items})
}

func (a *App) stoneImportApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.Header().Set("Allow","POST"); http.Error(w,"Método não permitido",405); return }
	user:=a.currentUser(r)
	if user==nil || (user.Role!="admin" && user.Role!="manager") { http.Error(w,"Somente administrador ou gerente pode importar produtos.",403); return }
	var body struct { Items []stoneCatalogItem `json:"items"` }
	if err:=json.NewDecoder(http.MaxBytesReader(w,r.Body,2<<20)).Decode(&body);err!=nil { http.Error(w,"Arquivo de importação inválido.",400);return }
	if len(body.Items)==0 || len(body.Items)>1000 { http.Error(w,"Selecione entre 1 e 1000 produtos por importação.",400);return }
	tx,err:=a.db.Begin()
	if err!=nil { http.Error(w,"Não foi possível iniciar a importação.",500);return }
	defer tx.Rollback()
	imported,skipped:=0,0
	for _, item := range body.Items {
		item.Name=strings.TrimSpace(item.Name); item.Barcode=strings.TrimSpace(item.Barcode); item.Category=strings.TrimSpace(item.Category); item.ImageURL=strings.TrimSpace(item.ImageURL)
		if item.Name=="" || len(item.Name)>200 || item.Price<0 || item.Price>1e9 { http.Error(w,"Produto com nome ou preço inválido; nenhum item foi gravado.",400);return }
		if item.ImageURL!="" {
			img,err:=url.Parse(item.ImageURL)
			if err!=nil || (img.Scheme!="https" && img.Scheme!="http") || img.Host=="" { item.ImageURL="" }
		}
		var exists int
		if item.Barcode!="" {
			err=tx.QueryRow("SELECT COUNT(*) FROM products WHERE barcode=? AND barcode<>''",item.Barcode).Scan(&exists)
		} else {
			err=tx.QueryRow("SELECT COUNT(*) FROM products WHERE name=? COLLATE NOCASE",item.Name).Scan(&exists)
		}
		if err!=nil { http.Error(w,"Erro ao verificar duplicidades.",500);return }
		if exists>0 { skipped++;continue }
		var categoryID int64
		if item.Category!="" {
			if len(item.Category)>100 { item.Category=item.Category[:100] }
			err=tx.QueryRow("SELECT id FROM categories WHERE name=? COLLATE NOCASE",item.Category).Scan(&categoryID)
			if err!=nil {
				res,e:=tx.Exec("INSERT INTO categories(name) VALUES(?)",item.Category)
				if e!=nil { http.Error(w,"Não foi possível criar uma categoria.",500);return }
				categoryID,_=res.LastInsertId()
			}
		}
		sum:=sha256.Sum256([]byte(strings.ToLower(item.Name)+"|"+item.Barcode))
		base:="STN"+strings.ToUpper(hex.EncodeToString(sum[:4]))
		code:=base
		for n:=1;;n++ {
			var count int
			if e:=tx.QueryRow("SELECT COUNT(*) FROM products WHERE code=?",code).Scan(&count);e!=nil { http.Error(w,"Erro ao gerar código do produto.",500);return }
			if count==0 { break }
			code=fmt.Sprintf("%s-%d",base,n)
		}
		_,err=tx.Exec("INSERT INTO products(code,name,unit,price_cents,cost_cents,barcode,category_id,stock,active,image_url) VALUES(?,?,'UN',?,0,?,NULLIF(?,0),0,1,?)",code,item.Name,int64(item.Price*100+0.5),item.Barcode,categoryID,item.ImageURL)
		if err!=nil { http.Error(w,"Não foi possível salvar um produto; a importação foi cancelada.",409);return }
		imported++
	}
	if _,err=tx.Exec("INSERT INTO audit_log(user_id,action,entity_type,entity_id,details) VALUES(?,?,?,?,?)",user.ID,"stone_catalog_import","product","",fmt.Sprintf("Importação de catálogo Stone: %d inseridos, %d ignorados por duplicidade",imported,skipped));err!=nil { http.Error(w,"Erro ao registrar auditoria; importação cancelada.",500);return }
	if err=tx.Commit();err!=nil { http.Error(w,"Não foi possível confirmar a importação.",500);return }
	jsonOut(w,200,map[string]any{"imported":imported,"skipped":skipped})
}
