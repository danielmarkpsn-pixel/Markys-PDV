package saas

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type POSHandler struct { DB *sql.DB; Auth *HTTPHandler }

func NewPOSHandler(db *sql.DB, auth *HTTPHandler) *POSHandler { return &POSHandler{DB:db,Auth:auth} }

func (h *POSHandler) Register(mux *http.ServeMux) {
	mux.Handle("/api/pos/cash",h.Auth.RequireSession(http.HandlerFunc(h.cashStatus)))
	mux.Handle("/api/pos/cash/open",h.Auth.RequireSession(http.HandlerFunc(h.openCash)))
	mux.Handle("/api/pos/cash/close",h.Auth.RequireSession(http.HandlerFunc(h.closeCash)))
	mux.Handle("/api/pos/sales",h.Auth.RequireSession(http.HandlerFunc(h.sales)))
}

func posOperator(w http.ResponseWriter,r *http.Request)(SessionIdentity,bool){
	id,ok:=IdentityFromContext(r.Context())
	if !ok{writeJSON(w,401,map[string]string{"error":"Sessão inválida"});return SessionIdentity{},false}
	if id.Role!="owner"&&id.Role!="admin"&&id.Role!="manager"&&id.Role!="cashier"{writeJSON(w,403,map[string]string{"error":"Seu perfil não pode operar o caixa"});return SessionIdentity{},false}
	return id,true
}

type cashSessionView struct {
	ID string `json:"id"`
	TerminalCode string `json:"terminal_code"`
	OpeningAmountCents int64 `json:"opening_amount_cents"`
	ExpectedCashCents int64 `json:"expected_cash_cents"`
	OpenedAt time.Time `json:"opened_at"`
}

func (h *POSHandler) cashStatus(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodGet{w.Header().Set("Allow",http.MethodGet);writeJSON(w,405,map[string]string{"error":"Método não permitido"});return}
	id,ok:=IdentityFromContext(r.Context());if !ok{writeJSON(w,401,map[string]string{"error":"Sessão inválida"});return}
	terminal:=strings.TrimSpace(r.URL.Query().Get("terminal_code"))
	var session *cashSessionView
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		var x cashSessionView
		err:=tx.QueryRowContext(r.Context(),`SELECT id::text,terminal_code,opening_amount_cents,opened_at
			FROM tenant_cash_sessions WHERE tenant_id=$1::uuid AND status='open'
			AND ($2='' OR terminal_code=$2) ORDER BY opened_at DESC LIMIT 1`,id.TenantID,terminal).Scan(&x.ID,&x.TerminalCode,&x.OpeningAmountCents,&x.OpenedAt)
		if err==sql.ErrNoRows{return nil};if err!=nil{return err}
		err=tx.QueryRowContext(r.Context(),`SELECT $2 + COALESCE(SUM(p.amount_cents) FILTER (WHERE p.method='cash'),0)
			FROM tenant_payments p JOIN tenant_sales s ON s.tenant_id=p.tenant_id AND s.id=p.sale_id
			WHERE s.tenant_id=$1::uuid AND s.cash_session_id=$3::uuid AND s.status='completed'`,id.TenantID,x.OpeningAmountCents,x.ID).Scan(&x.ExpectedCashCents)
		if err!=nil{return err};session=&x;return nil
	})
	if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível consultar o caixa"});return}
	writeJSON(w,200,map[string]any{"session":session})
}

func (h *POSHandler) openCash(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{w.Header().Set("Allow",http.MethodPost);writeJSON(w,405,map[string]string{"error":"Método não permitido"});return}
	id,ok:=posOperator(w,r);if !ok{return}
	var in struct{TerminalCode string `json:"terminal_code"`;OpeningAmountCents int64 `json:"opening_amount_cents"`}
	if !decodeAuthJSON(w,r,&in){return};in.TerminalCode=strings.TrimSpace(in.TerminalCode)
	if in.TerminalCode==""||len(in.TerminalCode)>40||in.OpeningAmountCents<0||in.OpeningAmountCents>1_000_000_000_000{writeJSON(w,400,map[string]string{"error":"Informe terminal e valor inicial válido"});return}
	var sessionID string
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		return tx.QueryRowContext(r.Context(),`INSERT INTO tenant_cash_sessions(tenant_id,terminal_code,opened_by,opening_amount_cents,status)
			VALUES($1::uuid,$2,$3::uuid,$4,'open') RETURNING id::text`,id.TenantID,in.TerminalCode,id.UserID,in.OpeningAmountCents).Scan(&sessionID)
	})
	if err!=nil{writeJSON(w,409,map[string]string{"error":"Este terminal já possui um caixa aberto ou os dados são inválidos"});return}
	writeJSON(w,201,map[string]string{"id":sessionID,"status":"open"})
}

func (h *POSHandler) closeCash(w http.ResponseWriter,r *http.Request){
	if r.Method!=http.MethodPost{w.Header().Set("Allow",http.MethodPost);writeJSON(w,405,map[string]string{"error":"Método não permitido"});return}
	id,ok:=posOperator(w,r);if !ok{return}
	var in struct{SessionID string `json:"session_id"`;ClosingAmountCents int64 `json:"closing_amount_cents"`}
	if !decodeAuthJSON(w,r,&in){return};in.SessionID=strings.TrimSpace(in.SessionID)
	if in.SessionID==""||in.ClosingAmountCents<0||in.ClosingAmountCents>1_000_000_000_000{writeJSON(w,400,map[string]string{"error":"Informe sessão e valor contado válido"});return}
	var expected int64
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		var opening int64
		if err:=tx.QueryRowContext(r.Context(),`SELECT opening_amount_cents FROM tenant_cash_sessions WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='open' FOR UPDATE`,id.TenantID,in.SessionID).Scan(&opening);err!=nil{return err}
		if err:=tx.QueryRowContext(r.Context(),`SELECT $2 + COALESCE(SUM(p.amount_cents) FILTER (WHERE p.method='cash'),0)
			FROM tenant_payments p JOIN tenant_sales s ON s.tenant_id=p.tenant_id AND s.id=p.sale_id
			WHERE s.tenant_id=$1::uuid AND s.cash_session_id=$3::uuid AND s.status='completed'`,id.TenantID,opening,in.SessionID).Scan(&expected);err!=nil{return err}
		difference:=in.ClosingAmountCents-expected
		_,err:=tx.ExecContext(r.Context(),`UPDATE tenant_cash_sessions SET status='closed',closed_by=$3::uuid,closed_at=now(),closing_amount_cents=$4,expected_cash_cents=$5,cash_difference_cents=$6
			WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='open'`,id.TenantID,in.SessionID,id.UserID,in.ClosingAmountCents,expected,difference)
		return err
	})
	if err!=nil{if err==sql.ErrNoRows{writeJSON(w,404,map[string]string{"error":"Caixa aberto não encontrado"});return};writeJSON(w,409,map[string]string{"error":"Não foi possível fechar este caixa"});return}
	writeJSON(w,200,map[string]any{"ok":true,"expected_cash_cents":expected,"closing_cash_cents":in.ClosingAmountCents,"difference_cents":in.ClosingAmountCents-expected})
}

type saleItemInput struct{ProductID string `json:"product_id"`;Quantity float64 `json:"quantity"`;DiscountCents int64 `json:"discount_cents"`}
type salePaymentInput struct{Method string `json:"method"`;AmountCents int64 `json:"amount_cents"`;ExternalReference string `json:"external_reference"`}
type saleInput struct{CashSessionID string `json:"cash_session_id"`;CustomerID string `json:"customer_id"`;Items []saleItemInput `json:"items"`;Payments []salePaymentInput `json:"payments"`}

func (h *POSHandler) sales(w http.ResponseWriter,r *http.Request){
	id,ok:=posOperator(w,r);if !ok{return}
	switch r.Method{
	case http.MethodGet:
		limit:=50;if raw:=r.URL.Query().Get("limit");raw!=""{n,err:=strconv.Atoi(raw);if err!=nil||n<1||n>200{writeJSON(w,400,map[string]string{"error":"Limite deve estar entre 1 e 200"});return};limit=n}
		items:=[]map[string]any{}
		err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
			rows,err:=tx.QueryContext(r.Context(),`SELECT s.id::text,s.sale_number,s.status,s.subtotal_cents,s.discount_cents,s.total_cents,s.created_at,u.display_name
				FROM tenant_sales s LEFT JOIN tenant_users u ON u.tenant_id=s.tenant_id AND u.id=s.operator_id
				WHERE s.tenant_id=$1::uuid ORDER BY s.created_at DESC LIMIT $2`,id.TenantID,limit)
			if err!=nil{return err};defer rows.Close()
			for rows.Next(){var saleID,number,status,operator string;var subtotal,discount,total int64;var created time.Time;if err:=rows.Scan(&saleID,&number,&status,&subtotal,&discount,&total,&created,&operator);err!=nil{return err};items=append(items,map[string]any{"id":saleID,"sale_number":number,"status":status,"subtotal_cents":subtotal,"discount_cents":discount,"total_cents":total,"created_at":created,"operator":operator})};return rows.Err()
		})
		if err!=nil{writeJSON(w,500,map[string]string{"error":"Não foi possível listar vendas"});return};writeJSON(w,200,map[string]any{"sales":items,"limit":limit})
	case http.MethodPost:
		h.createSale(w,r,id)
	default:
		w.Header().Set("Allow","GET, POST");writeJSON(w,405,map[string]string{"error":"Método não permitido"})
	}
}

func (h *POSHandler) createSale(w http.ResponseWriter,r *http.Request,id SessionIdentity){
	var in saleInput;if !decodeAuthJSON(w,r,&in){return}
	in.CashSessionID=strings.TrimSpace(in.CashSessionID);in.CustomerID=strings.TrimSpace(in.CustomerID)
	if in.CashSessionID==""||len(in.Items)==0||len(in.Items)>100||len(in.Payments)>10{writeJSON(w,400,map[string]string{"error":"Informe caixa aberto e de 1 a 100 itens válidos"});return}
	for i:=range in.Items{
		in.Items[i].ProductID=strings.TrimSpace(in.Items[i].ProductID)
		if in.Items[i].ProductID==""||math.IsNaN(in.Items[i].Quantity)||math.IsInf(in.Items[i].Quantity,0)||in.Items[i].Quantity<=0||in.Items[i].Quantity>1_000_000||in.Items[i].DiscountCents<0{writeJSON(w,400,map[string]string{"error":"Item ou quantidade inválida"});return}
		in.Items[i].Quantity=math.Round(in.Items[i].Quantity*1000)/1000
	}
	paymentTotal:=int64(0)
	allowed:=map[string]bool{"cash":true,"pix":true,"debit":true,"credit":true,"voucher":true,"other":true}
	for i:=range in.Payments{
		in.Payments[i].Method=strings.ToLower(strings.TrimSpace(in.Payments[i].Method))
		if !allowed[in.Payments[i].Method]||in.Payments[i].AmountCents<=0||len(in.Payments[i].ExternalReference)>160||paymentTotal>9_000_000_000_000_000-in.Payments[i].AmountCents{writeJSON(w,400,map[string]string{"error":"Forma ou valor de pagamento inválido"});return}
		paymentTotal+=in.Payments[i].AmountCents
	}
	var saleID,saleNumber string
	var subtotal,discount,total int64
	err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
		var cashStatus string
		if err:=tx.QueryRowContext(r.Context(),`SELECT status FROM tenant_cash_sessions WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`,id.TenantID,in.CashSessionID).Scan(&cashStatus);err!=nil{return err}
		if cashStatus!="open"{return errInvalidUserInput}
		saleNumber="PDV-"+time.Now().UTC().Format("20060102-150405.000000000")
		if err:=tx.QueryRowContext(r.Context(),`INSERT INTO tenant_sales(tenant_id,sale_number,cash_session_id,customer_id,operator_id,status)
			VALUES($1::uuid,$2,$3::uuid,NULLIF($4,'')::uuid,$5::uuid,'draft') RETURNING id::text`,id.TenantID,saleNumber,in.CashSessionID,in.CustomerID,id.UserID).Scan(&saleID);err!=nil{return err}
		for _,item:=range in.Items{
			var code,name,unit string;var price int64;var stock float64;var trackLot bool
			err:=tx.QueryRowContext(r.Context(),`SELECT code,name,unit,price_cents,stock,track_lot FROM tenant_products
				WHERE tenant_id=$1::uuid AND id=$2::uuid AND active=true FOR UPDATE`,id.TenantID,item.ProductID).Scan(&code,&name,&unit,&price,&stock,&trackLot)
			if err!=nil{return err}
			if price<0||price>1_000_000_000_000{return errInvalidUserInput}
			if stock+0.000001<item.Quantity{return errInsufficientStock}
			gross:=int64(math.Round(item.Quantity*float64(price)))
			if gross<0||gross>9_000_000_000_000_000||item.DiscountCents>gross||subtotal>9_000_000_000_000_000-gross||discount>9_000_000_000_000_000-item.DiscountCents{return errInvalidUserInput}
			lineTotal:=gross-item.DiscountCents
			if total>9_000_000_000_000_000-lineTotal{return errInvalidUserInput}
			subtotal+=gross;discount+=item.DiscountCents;total+=lineTotal
			if trackLot{
				remaining:=item.Quantity
				rows,err:=tx.QueryContext(r.Context(),`SELECT id::text,lot_code,quantity FROM tenant_product_lots
					WHERE tenant_id=$1::uuid AND product_id=$2::uuid AND quantity>0
					AND (expires_on IS NULL OR expires_on>=CURRENT_DATE)
					ORDER BY expires_on ASC NULLS LAST,created_at ASC FOR UPDATE`,id.TenantID,item.ProductID)
				if err!=nil{return err}
				type lot struct{ID,Code string;Quantity float64};lots:=[]lot{}
				for rows.Next(){var l lot;if err:=rows.Scan(&l.ID,&l.Code,&l.Quantity);err!=nil{rows.Close();return err};lots=append(lots,l)}
				if err:=rows.Err();err!=nil{rows.Close();return err};rows.Close()
				for _,l:=range lots{
					if remaining<=0.000001{break}
					take:=math.Min(remaining,l.Quantity)
					if _,err:=tx.ExecContext(r.Context(),`UPDATE tenant_product_lots SET quantity=quantity-$4 WHERE tenant_id=$1::uuid AND product_id=$2::uuid AND id=$3::uuid`,id.TenantID,item.ProductID,l.ID,take);err!=nil{return err}
					if _,err:=tx.ExecContext(r.Context(),`INSERT INTO tenant_stock_movements(tenant_id,product_id,movement_type,quantity,reference_type,reference_id,user_id,note)
						VALUES($1::uuid,$2::uuid,'sale',$3,'lot',$4::uuid,$5::uuid,$6)`,id.TenantID,item.ProductID,-take,l.ID,id.UserID,"Venda "+saleNumber);err!=nil{return err}
					remaining-=take
				}
				if remaining>0.000001{return errInsufficientStock}
			}else{
				if _,err:=tx.ExecContext(r.Context(),`INSERT INTO tenant_stock_movements(tenant_id,product_id,movement_type,quantity,reference_type,reference_id,user_id,note)
					VALUES($1::uuid,$2::uuid,'sale',$3,'sale',$4::uuid,$5::uuid,$6)`,id.TenantID,item.ProductID,-item.Quantity,saleID,id.UserID,"Venda "+saleNumber);err!=nil{return err}
			}
			if _,err:=tx.ExecContext(r.Context(),`UPDATE tenant_products SET stock=stock-$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`,id.TenantID,item.ProductID,item.Quantity);err!=nil{return err}
			if _,err:=tx.ExecContext(r.Context(),`INSERT INTO tenant_sale_items(tenant_id,sale_id,product_id,description,quantity,unit_price_cents,discount_cents,line_total_cents)
				VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8)`,id.TenantID,saleID,item.ProductID,name,item.Quantity,price,item.DiscountCents,lineTotal);err!=nil{return err}
			_ = code; _ = unit
		}
		if paymentTotal!=total{return errInvalidUserInput}
		for _,payment:=range in.Payments{
			if _,err:=tx.ExecContext(r.Context(),`INSERT INTO tenant_payments(tenant_id,sale_id,method,amount_cents,external_reference)
				VALUES($1::uuid,$2::uuid,$3,$4,$5)`,id.TenantID,saleID,payment.Method,payment.AmountCents,strings.TrimSpace(payment.ExternalReference));err!=nil{return err}
		}
		_,err:=tx.ExecContext(r.Context(),`UPDATE tenant_sales SET status='completed',subtotal_cents=$3,discount_cents=$4,total_cents=$5,completed_at=now()
			WHERE tenant_id=$1::uuid AND id=$2::uuid`,id.TenantID,saleID,subtotal,discount,total)
		return err
	})
	if err!=nil{
		if err==sql.ErrNoRows{writeJSON(w,404,map[string]string{"error":"Caixa, produto ou cliente não encontrado"});return}
		if err==errInsufficientStock{writeJSON(w,409,map[string]string{"error":"Estoque insuficiente para concluir a venda"});return}
		if err==errInvalidUserInput{writeJSON(w,400,map[string]string{"error":"Confira caixa aberto, descontos, pagamentos e dados da venda"});return}
		writeJSON(w,409,map[string]string{"error":"Venda não concluída; confira os dados e tente novamente"});return
	}
	writeJSON(w,201,map[string]any{"id":saleID,"sale_number":saleNumber,"status":"completed","subtotal_cents":subtotal,"discount_cents":discount,"total_cents":total})
}

var _ = fmt.Sprintf
