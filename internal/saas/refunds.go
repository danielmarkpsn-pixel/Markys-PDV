package saas

import (
 "database/sql"
 "errors"
 "net/http"
 "strings"
)

var errRefundMovementMissing = errors.New("sale stock movements are incomplete")
var errRefundCashSessionClosed = errors.New("cash refund requires an open cash session")

func (h *POSHandler) refundSale(w http.ResponseWriter, r *http.Request) {
 if r.Method != http.MethodPost { w.Header().Set("Allow",http.MethodPost); writeJSON(w,405,map[string]string{"error":"Método não permitido"}); return }
 id,ok:=posOperator(w,r); if !ok{return}
 if id.Role!="owner"&&id.Role!="admin"&&id.Role!="manager" { writeJSON(w,403,map[string]string{"error":"Seu perfil não pode estornar vendas"}); return }
 saleID:=strings.Trim(strings.TrimPrefix(r.URL.Path,"/api/pos/sales/"),"/")
 if saleID==""||strings.Contains(saleID,"/"){writeJSON(w,404,map[string]string{"error":"Venda não encontrada"});return}
 var in struct{Reason string `json:"reason"`}
 if !decodeAuthJSON(w,r,&in){return};in.Reason=strings.TrimSpace(in.Reason)
 if len(in.Reason)<5||len(in.Reason)>500{writeJSON(w,400,map[string]string{"error":"Informe um motivo de estorno entre 5 e 500 caracteres"});return}
 var refundID,saleNumber string;var total int64
 err:=WithTenantTx(r.Context(),h.DB,id.TenantID,nil,func(tx *sql.Tx)error{
  var status string
  var cashStatus sql.NullString
  if err:=tx.QueryRowContext(r.Context(),`SELECT s.sale_number,s.status,s.total_cents,cs.status
   FROM tenant_sales s JOIN tenant_cash_sessions cs ON cs.tenant_id=s.tenant_id AND cs.id=s.cash_session_id
   WHERE s.tenant_id=$1::uuid AND s.id=$2::uuid FOR UPDATE OF s,cs`,id.TenantID,saleID).Scan(&saleNumber,&status,&total,&cashStatus);err!=nil{return err}
  if status!="completed"||total<=0{return errInvalidUserInput}
  rows,err:=tx.QueryContext(r.Context(),`SELECT product_id::text,quantity,reference_type,reference_id::text FROM tenant_stock_movements
   WHERE tenant_id=$1::uuid AND sale_id=$2::uuid AND movement_type='sale' ORDER BY created_at,id FOR UPDATE`,id.TenantID,saleID)
  if err!=nil{return err}
  type movement struct{ProductID string;Quantity float64;ReferenceType string;ReferenceID sql.NullString}
  movements:=[]movement{}
  for rows.Next(){var m movement;if err:=rows.Scan(&m.ProductID,&m.Quantity,&m.ReferenceType,&m.ReferenceID);err!=nil{rows.Close();return err};movements=append(movements,m)}
  if err:=rows.Err();err!=nil{rows.Close();return err};rows.Close()
  if len(movements)==0{return errRefundMovementMissing}
  if err:=tx.QueryRowContext(r.Context(),`INSERT INTO tenant_sale_refunds(tenant_id,sale_id,refunded_by,reason,total_cents)
   VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5) RETURNING id::text`,id.TenantID,saleID,id.UserID,in.Reason,total).Scan(&refundID);err!=nil{return err}
  for _,m:=range movements{
   if m.Quantity>=0{return errRefundMovementMissing};restore:=-m.Quantity
   if m.ReferenceType=="lot"{
    if !m.ReferenceID.Valid{return errRefundMovementMissing}
    result,err:=tx.ExecContext(r.Context(),`UPDATE tenant_product_lots SET quantity=quantity+$4 WHERE tenant_id=$1::uuid AND product_id=$2::uuid AND id=$3::uuid`,id.TenantID,m.ProductID,m.ReferenceID.String,restore)
    if err!=nil{return err};n,err:=result.RowsAffected();if err!=nil{return err};if n!=1{return errRefundMovementMissing}
   }else if m.ReferenceType!="sale"{return errRefundMovementMissing}
   if _,err:=tx.ExecContext(r.Context(),`UPDATE tenant_products SET stock=stock+$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`,id.TenantID,m.ProductID,restore);err!=nil{return err}
   var reference any
   if m.ReferenceID.Valid{reference=m.ReferenceID.String}
   if _,err:=tx.ExecContext(r.Context(),`INSERT INTO tenant_stock_movements(tenant_id,product_id,movement_type,quantity,reference_type,reference_id,user_id,note,sale_id)
    VALUES($1::uuid,$2::uuid,'refund',$3,$4,$5::uuid,$6::uuid,$7,$8::uuid)`,id.TenantID,m.ProductID,restore,m.ReferenceType,reference,id.UserID,"Estorno "+saleNumber+" | "+in.Reason,saleID);err!=nil{return err}
  }
  payments,err:=tx.QueryContext(r.Context(),`SELECT id::text,method,amount_cents FROM tenant_payments WHERE tenant_id=$1::uuid AND sale_id=$2::uuid ORDER BY created_at,id FOR UPDATE`,id.TenantID,saleID)
  if err!=nil{return err}
  type payment struct{ID,Method string;Amount int64};originals:=[]payment{}
  for payments.Next(){var p payment;if err:=payments.Scan(&p.ID,&p.Method,&p.Amount);err!=nil{payments.Close();return err};originals=append(originals,p)}
  if err:=payments.Err();err!=nil{payments.Close();return err};payments.Close()
  if len(originals)==0{return errInvalidUserInput}
  for _,p:=range originals {
   if p.Method=="cash" && (!cashStatus.Valid || cashStatus.String!="open") { return errRefundCashSessionClosed }
  }
  var paymentTotal int64
  for _,p:=range originals{paymentTotal+=p.Amount;if _,err:=tx.ExecContext(r.Context(),`INSERT INTO tenant_sale_refund_payments(tenant_id,refund_id,original_payment_id,method,amount_cents)
   VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5)`,id.TenantID,refundID,p.ID,p.Method,p.Amount);err!=nil{return err}}
  if paymentTotal!=total{return errInvalidUserInput}
  if _,err:=tx.ExecContext(r.Context(),`UPDATE tenant_sales SET status='refunded' WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='completed'`,id.TenantID,saleID);err!=nil{return err}
  _,err=tx.ExecContext(r.Context(),`INSERT INTO tenant_audit_events(tenant_id,actor_user_id,event_type,entity_type,entity_id,details)
   VALUES($1::uuid,$2::uuid,'sale.refunded','sale',$3,jsonb_build_object('refund_id',$4,'reason',$5,'total_cents',$6))`,id.TenantID,id.UserID,saleID,refundID,in.Reason,total)
  return err
 })
 if err!=nil{
  switch err{case sql.ErrNoRows:writeJSON(w,404,map[string]string{"error":"Venda não encontrada"})
  case errInvalidUserInput:writeJSON(w,409,map[string]string{"error":"A venda não está concluída ou os pagamentos não conferem"})
  case errRefundMovementMissing:writeJSON(w,409,map[string]string{"error":"Esta venda não possui rastreabilidade completa de estoque para estorno seguro"})
  case errRefundCashSessionClosed:writeJSON(w,409,map[string]string{"error":"O caixa desta venda está fechado. Reabra um caixa autorizado antes de devolver valores em dinheiro."})
  default:writeJSON(w,409,map[string]string{"error":"Estorno não realizado; a operação foi revertida integralmente"})}
  return
 }
 writeJSON(w,200,map[string]any{"ok":true,"refund_id":refundID,"sale_id":saleID,"sale_number":saleNumber,"status":"refunded","total_cents":total,"reason":in.Reason})
}
