package saas

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

type CatalogHandler struct { DB *sql.DB; Auth *HTTPHandler }

func NewCatalogHandler(db *sql.DB,auth *HTTPHandler)*CatalogHandler{return &CatalogHandler{DB:db,Auth:auth}}

func (h *CatalogHandler) Register(mux *http.ServeMux){
	for _,resource:=range []string{"categories","suppliers","products"}{
		base:="/api/catalog/"+resource
		mux.Handle(base,h.Auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){h.collection(resource,w,r)})))
		mux.Handle(base+"/",h.Auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){h.item(resource,w,r)})))
	}
}

func catalogManager(w http.ResponseWriter,r *http.Request)(SessionIdentity,bool){
	id,ok:=IdentityFromContext(r.Context());if !ok{writeJSON(w,401,map[string]string{"error":"Sessão inválida"});return SessionIdentity{},false}
	if id.Role!="owner"&&id.Role!="admin"&&id.Role!="manager"{writeJSON(w,403,map[string]string{"error":"Seu perfil não pode administrar o catálogo"});return SessionIdentity{},false}
	return id,true
}

func(h *CatalogHandler)collection(resource string,w http.ResponseWriter,r *http.Request){
	id,ok:=IdentityFromContext(r.Context());if !ok{writeJSON(w,401,map[string]string{"error":"Sessão inválida"});return}
	if r.Method==http.MethodGet{
		if resource=="categories"{h.listCategories(w,r,id);return}
		if resource=="suppliers"{h.listSuppliers(w,r,id);return}
		h.listProducts(w,r,id);return
	}
	if r.Method!=http.MethodPost{w.Header().Set("Allow","GET, POST");writeJSON(w,405,map[string]string{"error":"Método não permitido"});return}
	id,ok=catalogManager(w,r);if !ok{return}
	switch resource{case "categories":h.createCategory(w,r,id);case "suppliers":h.createSupplier(w,r,id);case "products":h.createProduct(w,r,id)}
}

func(h *CatalogHandler)item(resource string,w http.ResponseWriter,r *http.Request){
	id,ok:=catalogManager(w,r);if !ok{return}
	itemID:=strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/catalog/"+resource+"/"),"/")
	if itemID==""||strings.Contains(itemID,"/"){writeJSON(w,404,map[string]string{"error":"Registro não encontrado"});return}
	if r.Method==http.MethodDelete{h.deactivate(w,r,id,resource,itemID);return}
	if r.Method!=http.MethodPut{w.Header().Set("Allow","PUT, DELETE");writeJSON(w,405,map[string]string{"error":"Método não permitido"});return}
	switch resource{case "categories":h.updateCategory(w,r,id,itemID);case "suppliers":h.updateSupplier(w,r,id,itemID);case "products":h.updateProduct(w,r,id,itemID)}
}

type catalogCategory struct{ID string `json:"id"`;Name string `json:"name"`;Active bool `json:"active"`;CreatedAt time.Time `json:"created_at"`}
func(h *CatalogHandler)listCategories(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	items:=[]catalogCategory{}
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		rows,err:=tx.QueryContext(r.Context(),"SELECT id::text,name,active,created_at FROM tenant_categories WHERE tenant_id=$1::uuid ORDER BY lower(name)",id.TenantID);if err!=nil{return err};defer rows.Close()
		for rows.Next(){var x catalogCategory;if err:=rows.Scan(&x.ID,&x.Name,&x.Active,&x.CreatedAt);err!=nil{return err};items=append(items,x)};return rows.Err()
	})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível listar categorias"});return};writeJSON(w,200,map[string]any{"categories":items})
}
func(h *CatalogHandler)createCategory(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	var in struct{Name string `json:"name"`};if !decodeAuthJSON(w,r,&in){return};in.Name=strings.TrimSpace(in.Name);if in.Name==""||len(in.Name)>120{writeJSON(w,400,map[string]string{"error":"Nome da categoria obrigatório (máximo 120 caracteres)"});return}
	var newID string;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{return tx.QueryRowContext(r.Context(),"INSERT INTO tenant_categories(tenant_id,name) VALUES($1::uuid,$2) RETURNING id::text",id.TenantID,in.Name).Scan(&newID)})
	if err!=nil{writeJSON(w,409,map[string]string{"error":"Categoria já cadastrada ou dados inválidos"});return};writeJSON(w,201,map[string]string{"id":newID})
}
func(h *CatalogHandler)updateCategory(w http.ResponseWriter,r *http.Request,id SessionIdentity,itemID string){
	var in struct{Name *string `json:"name"`;Active *bool `json:"active"`};if !decodeAuthJSON(w,r,&in){return};if in.Name==nil&&in.Active==nil{writeJSON(w,400,map[string]string{"error":"Nenhuma alteração informada"});return};if in.Name!=nil{v:=strings.TrimSpace(*in.Name);if v==""||len(v)>120{writeJSON(w,400,map[string]string{"error":"Nome inválido"});return};in.Name=&v}
	var result sql.Result;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{var e error;result,e=tx.ExecContext(r.Context(),"UPDATE tenant_categories SET name=COALESCE($3,name),active=COALESCE($4,active) WHERE tenant_id=$1::uuid AND id=$2::uuid",id.TenantID,itemID,in.Name,in.Active);return e})
	if err!=nil{writeJSON(w,409,map[string]string{"error":"Não foi possível atualizar categoria"});return};n,_:=result.RowsAffected();if n==0{writeJSON(w,404,map[string]string{"error":"Categoria não encontrada"});return};writeJSON(w,200,map[string]bool{"ok":true})
}

type catalogSupplier struct{ID string `json:"id"`;Name string `json:"name"`;Document string `json:"document"`;Phone string `json:"phone"`;Email string `json:"email"`;Active bool `json:"active"`;CreatedAt time.Time `json:"created_at"`}
func(h *CatalogHandler)listSuppliers(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	items:=[]catalogSupplier{}
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		rows,err:=tx.QueryContext(r.Context(),"SELECT id::text,name,document,phone,email,active,created_at FROM tenant_suppliers WHERE tenant_id=$1::uuid ORDER BY lower(name)",id.TenantID);if err!=nil{return err};defer rows.Close()
		for rows.Next(){var x catalogSupplier;if err:=rows.Scan(&x.ID,&x.Name,&x.Document,&x.Phone,&x.Email,&x.Active,&x.CreatedAt);err!=nil{return err};items=append(items,x)};return rows.Err()
	})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível listar fornecedores"});return};writeJSON(w,200,map[string]any{"suppliers":items})
}
func(h *CatalogHandler)createSupplier(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	var in struct{Name string `json:"name"`;Document string `json:"document"`;Phone string `json:"phone"`;Email string `json:"email"`};if !decodeAuthJSON(w,r,&in){return};in.Name=strings.TrimSpace(in.Name);if in.Name==""||len(in.Name)>180{writeJSON(w,400,map[string]string{"error":"Nome do fornecedor obrigatório (máximo 180 caracteres)"});return}
	var newID string;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{return tx.QueryRowContext(r.Context(),"INSERT INTO tenant_suppliers(tenant_id,name,document,phone,email) VALUES($1::uuid,$2,$3,$4,$5) RETURNING id::text",id.TenantID,in.Name,strings.TrimSpace(in.Document),strings.TrimSpace(in.Phone),strings.TrimSpace(in.Email)).Scan(&newID)})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível cadastrar fornecedor"});return};writeJSON(w,201,map[string]string{"id":newID})
}
func(h *CatalogHandler)updateSupplier(w http.ResponseWriter,r *http.Request,id SessionIdentity,itemID string){
	var in struct{Name *string `json:"name"`;Document *string `json:"document"`;Phone *string `json:"phone"`;Email *string `json:"email"`;Active *bool `json:"active"`};if !decodeAuthJSON(w,r,&in){return};if in.Name==nil&&in.Document==nil&&in.Phone==nil&&in.Email==nil&&in.Active==nil{writeJSON(w,400,map[string]string{"error":"Nenhuma alteração informada"});return};if in.Name!=nil{v:=strings.TrimSpace(*in.Name);if v==""||len(v)>180{writeJSON(w,400,map[string]string{"error":"Nome inválido"});return};in.Name=&v}
	var result sql.Result;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{var e error;result,e=tx.ExecContext(r.Context(),"UPDATE tenant_suppliers SET name=COALESCE($3,name),document=COALESCE($4,document),phone=COALESCE($5,phone),email=COALESCE($6,email),active=COALESCE($7,active) WHERE tenant_id=$1::uuid AND id=$2::uuid",id.TenantID,itemID,in.Name,in.Document,in.Phone,in.Email,in.Active);return e})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível atualizar fornecedor"});return};n,_:=result.RowsAffected();if n==0{writeJSON(w,404,map[string]string{"error":"Fornecedor não encontrado"});return};writeJSON(w,200,map[string]bool{"ok":true})
}

type catalogProduct struct{ID string `json:"id"`;Code string `json:"code"`;Name string `json:"name"`;Unit string `json:"unit"`;PriceCents int64 `json:"price_cents"`;CostCents int64 `json:"cost_cents"`;Barcode string `json:"barcode"`;CategoryID *string `json:"category_id"`;SupplierID *string `json:"supplier_id"`;CategoryName string `json:"category_name"`;SupplierName string `json:"supplier_name"`;NCM string `json:"ncm"`;CEST string `json:"cest"`;CFOP string `json:"cfop"`;TaxOrigin string `json:"tax_origin"`;TaxProfile string `json:"tax_profile"`;TrackLot bool `json:"track_lot"`;TrackExpiry bool `json:"track_expiry"`;WeighedItem bool `json:"weighed_item"`;Active bool `json:"active"`;ImageURL string `json:"image_url"`;CreatedAt time.Time `json:"created_at"`}
func(h *CatalogHandler)listProducts(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	q:="%"+strings.TrimSpace(r.URL.Query().Get("q"))+"%";items:=[]catalogProduct{}
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		rows,err:=tx.QueryContext(r.Context(),`SELECT p.id::text,p.code,p.name,p.unit,p.price_cents,p.cost_cents,p.barcode,p.category_id::text,p.supplier_id::text,COALESCE(c.name,''),COALESCE(s.name,''),p.ncm,p.cest,p.cfop,p.tax_origin,p.tax_profile,p.track_lot,p.track_expiry,p.weighed_item,p.active,p.image_url,p.created_at
		FROM tenant_products p LEFT JOIN tenant_categories c ON c.tenant_id=p.tenant_id AND c.id=p.category_id LEFT JOIN tenant_suppliers s ON s.tenant_id=p.tenant_id AND s.id=p.supplier_id
		WHERE p.tenant_id=$1::uuid AND ($2='%%' OR p.name ILIKE $2 OR p.code ILIKE $2 OR p.barcode ILIKE $2) ORDER BY lower(p.name) LIMIT 250`,id.TenantID,q);if err!=nil{return err};defer rows.Close()
		for rows.Next(){var x catalogProduct;var cat,sup sql.NullString;if err:=rows.Scan(&x.ID,&x.Code,&x.Name,&x.Unit,&x.PriceCents,&x.CostCents,&x.Barcode,&cat,&sup,&x.CategoryName,&x.SupplierName,&x.NCM,&x.CEST,&x.CFOP,&x.TaxOrigin,&x.TaxProfile,&x.TrackLot,&x.TrackExpiry,&x.WeighedItem,&x.Active,&x.ImageURL,&x.CreatedAt);err!=nil{return err};if cat.Valid{x.CategoryID=&cat.String};if sup.Valid{x.SupplierID=&sup.String};items=append(items,x)};return rows.Err()
	})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível listar produtos"});return};writeJSON(w,200,map[string]any{"products":items,"limit":250})
}
type productInput struct{Code *string `json:"code"`;Name *string `json:"name"`;Unit *string `json:"unit"`;PriceCents *int64 `json:"price_cents"`;CostCents *int64 `json:"cost_cents"`;Barcode *string `json:"barcode"`;CategoryID *string `json:"category_id"`;SupplierID *string `json:"supplier_id"`;NCM *string `json:"ncm"`;CEST *string `json:"cest"`;CFOP *string `json:"cfop"`;TaxOrigin *string `json:"tax_origin"`;TaxProfile *string `json:"tax_profile"`;TrackLot *bool `json:"track_lot"`;TrackExpiry *bool `json:"track_expiry"`;WeighedItem *bool `json:"weighed_item"`;ImageURL *string `json:"image_url"`}
func normalizeProduct(in *productInput)error{
	trim:=func(p **string){if *p!=nil{v:=strings.TrimSpace(**p);*p=&v}}
	trim(&in.Code);trim(&in.Name);trim(&in.Unit);trim(&in.Barcode);trim(&in.NCM);trim(&in.CEST);trim(&in.CFOP);trim(&in.TaxOrigin);trim(&in.TaxProfile);trim(&in.ImageURL)
	if in.PriceCents!=nil&&*in.PriceCents<0{return errInvalidUserInput};if in.CostCents!=nil&&*in.CostCents<0{return errInvalidUserInput}
	if in.Code!=nil&&(*in.Code==""||len(*in.Code)>80){return errInvalidUserInput};if in.Name!=nil&&(*in.Name==""||len(*in.Name)>240){return errInvalidUserInput};if in.Unit!=nil&&(*in.Unit==""||len(*in.Unit)>8){return errInvalidUserInput}
	return nil
}
func(h *CatalogHandler)createProduct(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	var in productInput;if !decodeAuthJSON(w,r,&in){return};if err:=normalizeProduct(&in);err!=nil||in.Code==nil||in.Name==nil{writeJSON(w,400,map[string]string{"error":"Código, nome, unidade e valores válidos são obrigatórios"});return}
	unit:="UN";if in.Unit!=nil{unit=*in.Unit};price:=int64(0);if in.PriceCents!=nil{price=*in.PriceCents};cost:=int64(0);if in.CostCents!=nil{cost=*in.CostCents}
	var newID string;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{return tx.QueryRowContext(r.Context(),`INSERT INTO tenant_products(tenant_id,code,name,unit,price_cents,cost_cents,barcode,category_id,supplier_id,ncm,cest,cfop,tax_origin,tax_profile,track_lot,track_expiry,weighed_item,image_url)
	VALUES($1::uuid,$2,$3,$4,$5,$6,COALESCE($7,''),NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,COALESCE($10,''),COALESCE($11,''),COALESCE($12,''),COALESCE($13,''),COALESCE($14,''),COALESCE($15,false),COALESCE($16,false),COALESCE($17,false),COALESCE($18,'')) RETURNING id::text`,id.TenantID,*in.Code,*in.Name,unit,price,cost,in.Barcode,in.CategoryID,in.SupplierID,in.NCM,in.CEST,in.CFOP,in.TaxOrigin,in.TaxProfile,in.TrackLot,in.TrackExpiry,in.WeighedItem,in.ImageURL).Scan(&newID)})
	if err!=nil{writeJSON(w,409,map[string]string{"error":"Produto não criado: código duplicado ou categoria/fornecedor inválido"});return};writeJSON(w,201,map[string]string{"id":newID})
}
func(h *CatalogHandler)updateProduct(w http.ResponseWriter,r *http.Request,id SessionIdentity,itemID string){
	var in productInput;if !decodeAuthJSON(w,r,&in){return};if err:=normalizeProduct(&in);err!=nil{writeJSON(w,400,map[string]string{"error":"Dados do produto inválidos"});return}
	if in.Code==nil&&in.Name==nil&&in.Unit==nil&&in.PriceCents==nil&&in.CostCents==nil&&in.Barcode==nil&&in.CategoryID==nil&&in.SupplierID==nil&&in.NCM==nil&&in.CEST==nil&&in.CFOP==nil&&in.TaxOrigin==nil&&in.TaxProfile==nil&&in.TrackLot==nil&&in.TrackExpiry==nil&&in.WeighedItem==nil&&in.ImageURL==nil{writeJSON(w,400,map[string]string{"error":"Nenhuma alteração informada"});return}
	var result sql.Result;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{var e error;result,e=tx.ExecContext(r.Context(),`UPDATE tenant_products SET code=COALESCE($3,code),name=COALESCE($4,name),unit=COALESCE($5,unit),price_cents=COALESCE($6,price_cents),cost_cents=COALESCE($7,cost_cents),barcode=COALESCE($8,barcode),category_id=COALESCE(NULLIF($9,'')::uuid,category_id),supplier_id=COALESCE(NULLIF($10,'')::uuid,supplier_id),ncm=COALESCE($11,ncm),cest=COALESCE($12,cest),cfop=COALESCE($13,cfop),tax_origin=COALESCE($14,tax_origin),tax_profile=COALESCE($15,tax_profile),track_lot=COALESCE($16,track_lot),track_expiry=COALESCE($17,track_expiry),weighed_item=COALESCE($18,weighed_item),image_url=COALESCE($19,image_url),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`,id.TenantID,itemID,in.Code,in.Name,in.Unit,in.PriceCents,in.CostCents,in.Barcode,in.CategoryID,in.SupplierID,in.NCM,in.CEST,in.CFOP,in.TaxOrigin,in.TaxProfile,in.TrackLot,in.TrackExpiry,in.WeighedItem,in.ImageURL);return e})
	if err!=nil{writeJSON(w,409,map[string]string{"error":"Não foi possível atualizar produto; verifique código, categoria e fornecedor"});return};n,_:=result.RowsAffected();if n==0{writeJSON(w,404,map[string]string{"error":"Produto não encontrado"});return};writeJSON(w,200,map[string]bool{"ok":true})
}
func(h *CatalogHandler)deactivate(w http.ResponseWriter,r *http.Request,id SessionIdentity,resource,itemID string){
	var table string;switch resource{case "categories":table="tenant_categories";case "suppliers":table="tenant_suppliers";case "products":table="tenant_products";default:writeJSON(w,404,map[string]string{"error":"Recurso não encontrado"});return}
	var result sql.Result;err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{var e error;result,e=tx.ExecContext(r.Context(),"UPDATE "+table+" SET active=false WHERE tenant_id=$1::uuid AND id=$2::uuid",id.TenantID,itemID);return e})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível inativar registro"});return};n,_:=result.RowsAffected();if n==0{writeJSON(w,404,map[string]string{"error":"Registro não encontrado"});return};writeJSON(w,200,map[string]bool{"ok":true})
}
