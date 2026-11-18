package saas

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var errInsufficientStock = errors.New("insufficient stock")

type InventoryHandler struct {
	DB *sql.DB
	Auth *HTTPHandler
}

func NewInventoryHandler(db *sql.DB, auth *HTTPHandler) *InventoryHandler {
	return &InventoryHandler{DB: db, Auth: auth}
}

func (h *InventoryHandler) Register(mux *http.ServeMux) {
	mux.Handle("/api/inventory/stock", h.Auth.RequireSession(http.HandlerFunc(h.stock)))
	mux.Handle("/api/inventory/movements", h.Auth.RequireSession(http.HandlerFunc(h.movements)))
	mux.Handle("/api/inventory/adjustments", h.Auth.RequireSession(http.HandlerFunc(h.adjust)))
}

func inventoryManager(w http.ResponseWriter, r *http.Request) (SessionIdentity, bool) {
	id, ok := IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Sessão inválida"})
		return SessionIdentity{}, false
	}
	if id.Role != "owner" && id.Role != "admin" && id.Role != "manager" && id.Role != "stockkeeper" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Seu perfil não pode movimentar estoque"})
		return SessionIdentity{}, false
	}
	return id, true
}

type stockRow struct {
	ProductID string `json:"product_id"`
	Code string `json:"code"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	Stock float64 `json:"stock"`
	TrackLot bool `json:"track_lot"`
	TrackExpiry bool `json:"track_expiry"`
}

func (h *InventoryHandler) stock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método não permitido"})
		return
	}
	id, ok := IdentityFromContext(r.Context())
	if !ok { writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Sessão inválida"}); return }
	query := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	items := []stockRow{}
	err := WithTenantTx(r.Context(), h.DB, id.TenantID, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(r.Context(), `SELECT id::text,code,name,unit,stock,track_lot,track_expiry
			FROM tenant_products WHERE tenant_id=$1::uuid AND active=true
			AND ($2='%%' OR code ILIKE $2 OR name ILIKE $2 OR barcode ILIKE $2)
			ORDER BY lower(name) LIMIT 500`, id.TenantID, query)
		if err != nil { return err }
		defer rows.Close()
		for rows.Next() {
			var item stockRow
			if err := rows.Scan(&item.ProductID,&item.Code,&item.Name,&item.Unit,&item.Stock,&item.TrackLot,&item.TrackExpiry); err != nil { return err }
			items = append(items,item)
		}
		return rows.Err()
	})
	if err != nil { writeJSON(w,http.StatusInternalServerError,map[string]string{"error":"Não foi possível consultar o estoque"}); return }
	writeJSON(w,http.StatusOK,map[string]any{"items":items,"limit":500})
}

type movementRow struct {
	ID string `json:"id"`
	ProductID string `json:"product_id"`
	ProductCode string `json:"product_code"`
	ProductName string `json:"product_name"`
	MovementType string `json:"movement_type"`
	Quantity float64 `json:"quantity"`
	Note string `json:"note"`
	LotCode string `json:"lot_code"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *InventoryHandler) movements(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w,http.StatusMethodNotAllowed,map[string]string{"error":"Método não permitido"})
		return
	}
	id,ok:=IdentityFromContext(r.Context());if !ok{writeJSON(w,401,map[string]string{"error":"Sessão inválida"});return}
	limit:=100
	if raw:=r.URL.Query().Get("limit");raw!=""{n,err:=strconv.Atoi(raw);if err!=nil||n<1||n>500{writeJSON(w,400,map[string]string{"error":"Limite deve estar entre 1 e 500"});return};limit=n}
	items:=[]movementRow{}
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		rows,err:=tx.QueryContext(r.Context(),`SELECT m.id::text,m.product_id::text,p.code,p.name,m.movement_type,m.quantity,m.note,COALESCE(l.lot_code,''),m.created_at
			FROM tenant_stock_movements m JOIN tenant_products p ON p.tenant_id=m.tenant_id AND p.id=m.product_id
			LEFT JOIN tenant_product_lots l ON l.tenant_id=m.tenant_id AND l.product_id=m.product_id AND m.reference_type='lot' AND l.id=m.reference_id
			WHERE m.tenant_id=$1::uuid ORDER BY m.created_at DESC LIMIT $2`,id.TenantID,limit)
		if err!=nil{return err};defer rows.Close()
		for rows.Next(){var x movementRow;if err:=rows.Scan(&x.ID,&x.ProductID,&x.ProductCode,&x.ProductName,&x.MovementType,&x.Quantity,&x.Note,&x.LotCode,&x.CreatedAt);err!=nil{return err};items=append(items,x)}
		return rows.Err()
	})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível consultar movimentações"});return}
	writeJSON(w,200,map[string]any{"movements":items,"limit":limit})
}

type stockAdjustmentInput struct {
	ProductID string `json:"product_id"`
	MovementType string `json:"movement_type"`
	Quantity float64 `json:"quantity"`
	LotCode string `json:"lot_code"`
	ExpiresOn string `json:"expires_on"`
	Note string `json:"note"`
}

func (h *InventoryHandler) adjust(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.Header().Set("Allow",http.MethodPost);writeJSON(w,405,map[string]string{"error":"Método não permitido"});return }
	id,ok:=inventoryManager(w,r);if !ok{return}
	var in stockAdjustmentInput
	if !decodeAuthJSON(w,r,&in){return}
	in.ProductID=strings.TrimSpace(in.ProductID);in.MovementType=strings.ToLower(strings.TrimSpace(in.MovementType));in.LotCode=strings.TrimSpace(in.LotCode);in.Note=strings.TrimSpace(in.Note)
	if in.ProductID==""||len(in.Note)>500||(in.MovementType!="entry"&&in.MovementType!="exit"&&in.MovementType!="count")||math.IsNaN(in.Quantity)||math.IsInf(in.Quantity,0)||in.Quantity<0||in.Quantity>1_000_000_000||(in.MovementType!="count"&&in.Quantity==0){
		writeJSON(w,400,map[string]string{"error":"Informe produto, tipo de movimento e quantidade válida"});return
	}
	in.Quantity=math.Round(in.Quantity*1000)/1000
	var expires any
	if in.ExpiresOn!="" { parsed,err:=time.Parse("2006-01-02",in.ExpiresOn);if err!=nil{writeJSON(w,400,map[string]string{"error":"Validade deve usar AAAA-MM-DD"});return};expires=parsed }
	var newID string
	var newStock float64
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		var current float64;var trackLot,trackExpiry bool
		err:=tx.QueryRowContext(r.Context(),`SELECT stock,track_lot,track_expiry FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid AND active=true FOR UPDATE`,id.TenantID,in.ProductID).Scan(&current,&trackLot,&trackExpiry)
		if err!=nil{return err}
		if in.MovementType=="count"&&trackLot{return errInvalidUserInput}
		if trackLot&&in.MovementType!="count"&&in.LotCode==""{return errInvalidUserInput}
		if trackExpiry&&trackLot&&in.MovementType=="entry"&&expires==nil{return errInvalidUserInput}
		delta:=in.Quantity
		if in.MovementType=="exit"{delta=-in.Quantity}
		if in.MovementType=="count"{delta=in.Quantity-current}
		if current+delta < -0.000001{return errInsufficientStock}
		var lotID any
		if trackLot&&in.MovementType!="count"{
			if in.MovementType=="entry"{
				var lotIDText string
				err=tx.QueryRowContext(r.Context(),`INSERT INTO tenant_product_lots(tenant_id,product_id,lot_code,expires_on,quantity)
					VALUES($1::uuid,$2::uuid,$3,$4,$5)
					ON CONFLICT(tenant_id,product_id,lot_code) DO UPDATE SET
					quantity=tenant_product_lots.quantity+EXCLUDED.quantity,
					expires_on=COALESCE(EXCLUDED.expires_on,tenant_product_lots.expires_on)
					RETURNING id::text`,id.TenantID,in.ProductID,in.LotCode,expires,in.Quantity).Scan(&lotIDText)
				if err!=nil{return err};lotID=lotIDText
			}else{
				var lotQuantity float64;var lotIDText string
				err=tx.QueryRowContext(r.Context(),`SELECT id::text,quantity FROM tenant_product_lots WHERE tenant_id=$1::uuid AND product_id=$2::uuid AND lot_code=$3 FOR UPDATE`,id.TenantID,in.ProductID,in.LotCode).Scan(&lotIDText,&lotQuantity)
				if err!=nil{return err};if lotQuantity+delta < -0.000001{return errInsufficientStock};lotID=lotIDText
				if _,err=tx.ExecContext(r.Context(),`UPDATE tenant_product_lots SET quantity=quantity+$4 WHERE tenant_id=$1::uuid AND product_id=$2::uuid AND id=$3::uuid`,id.TenantID,in.ProductID,lotIDText,delta);err!=nil{return err}
			}
		}
		if _,err=tx.ExecContext(r.Context(),`UPDATE tenant_products SET stock=GREATEST(0,stock+$3),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`,id.TenantID,in.ProductID,delta);err!=nil{return err}
		refType:="";if lotID!=nil{refType="lot"}
		return tx.QueryRowContext(r.Context(),`INSERT INTO tenant_stock_movements(tenant_id,product_id,movement_type,quantity,reference_type,reference_id,user_id,note)
			VALUES($1::uuid,$2::uuid,$3,$4,$5,NULLIF($6,'')::uuid,$7::uuid,$8) RETURNING id::text`,
			id.TenantID,in.ProductID,in.MovementType,delta,refType,lotID,id.UserID,in.Note).Scan(&newID)
	})
	if err!=nil{
		if err==sql.ErrNoRows{writeJSON(w,404,map[string]string{"error":"Produto não encontrado ou inativo"});return}
		if err==errInsufficientStock{writeJSON(w,409,map[string]string{"error":"Estoque insuficiente para esta saída"});return}
		if err==errInvalidUserInput{writeJSON(w,400,map[string]string{"error":"Este produto exige controle por lote. Informe lote e validade, ou faça a movimentação por lote."});return}
		writeJSON(w,409,map[string]string{"error":"Não foi possível registrar a movimentação; confira produto e lote"});return
	}
	err=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{return tx.QueryRowContext(r.Context(),"SELECT stock FROM tenant_products WHERE tenant_id=$1::uuid AND id=$2::uuid",id.TenantID,in.ProductID).Scan(&newStock)})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Movimentação registrada, mas não foi possível atualizar a resposta"});return}
	writeJSON(w,201,map[string]any{"id":newID,"stock":newStock})
}
