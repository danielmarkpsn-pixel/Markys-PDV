package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"errors"
	"net/http"
	"strings"
)

type cashRequest struct {
	Action string `json:"action"`
	Amount int64 `json:"amount_cents"`
	Terminal string `json:"terminal_code"`
}

func (a *App) cash(w http.ResponseWriter, r *http.Request) {
	userID:=requestUserID(a,r)
	switch r.Method {
	case http.MethodGet:
		var id, opening int64
		var terminal, openedAt string
		err := a.db.QueryRow(`SELECT id, terminal_code, opened_at, opening_amount_cents FROM cash_sessions WHERE status='open' ORDER BY id DESC LIMIT 1`).Scan(&id, &terminal, &openedAt, &opening)
		if errors.Is(err, sql.ErrNoRows) { jsonOut(w, 200, map[string]any{"open": false}); return }
		if err != nil { http.Error(w, "Erro ao consultar caixa", 500); return }
		totals, err := cashPaymentTotals(a.db, id)
		if err != nil { http.Error(w, "Erro ao calcular pagamentos do caixa", 500); return }
		jsonOut(w, 200, map[string]any{"open": true, "id": id, "terminal_code": terminal, "opened_at": openedAt, "opening_amount_cents": opening, "cash_received_cents": totals["cash"], "pix_received_cents": totals["pix"], "debit_received_cents": totals["debit"], "credit_received_cents": totals["credit"], "voucher_received_cents": totals["voucher"], "total_sales_received_cents": totals["cash"]+totals["pix"]+totals["debit"]+totals["credit"]+totals["voucher"], "expected_cash_cents": opening + totals["cash"]})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req cashRequest
		dec := json.NewDecoder(r.Body); dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil { http.Error(w, "JSON inválido", 400); return }
		if req.Amount < 0 || req.Amount > 1000000000000 { http.Error(w, "Valor inválido", 400); return }
		switch req.Action {
		case "open":
			terminal := strings.TrimSpace(req.Terminal); if terminal == "" { terminal = "PDV-01" }
			tx, err := a.db.Begin(); if err != nil { http.Error(w, "Erro ao abrir caixa", 500); return }; defer tx.Rollback()
			var count int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM cash_sessions WHERE status='open'`).Scan(&count); err != nil { http.Error(w, "Erro ao verificar caixa", 500); return }
			if count > 0 { http.Error(w, "Já existe um caixa aberto", 409); return }
			res, err := tx.Exec(`INSERT INTO cash_sessions(terminal_code, opened_by, opening_amount_cents, status) VALUES(?,?,?,'open')`, terminal, nullableUserID(userID), req.Amount); if err != nil { http.Error(w, "Não foi possível abrir caixa", 500); return }
			id, err := res.LastInsertId(); if err != nil { http.Error(w, "Erro ao abrir caixa", 500); return }
			if err = tx.Commit(); err != nil { http.Error(w, "Erro ao confirmar abertura", 500); return }
			a.writeAudit(userID,"cash_opened","cash_session",strconv.FormatInt(id,10),fmt.Sprintf("Caixa %s aberto com fundo inicial de %d centavos",terminal,req.Amount))
			jsonOut(w, 201, map[string]any{"open": true, "id": id, "terminal_code": terminal, "opening_amount_cents": req.Amount})
		case "close":
			tx, err := a.db.Begin(); if err != nil { http.Error(w, "Erro ao fechar caixa", 500); return }; defer tx.Rollback()
			var id, opening int64
			err = tx.QueryRow(`SELECT id, opening_amount_cents FROM cash_sessions WHERE status='open' ORDER BY id DESC LIMIT 1`).Scan(&id, &opening)
			if errors.Is(err, sql.ErrNoRows) { http.Error(w, "Não há caixa aberto", 409); return }; if err != nil { http.Error(w, "Erro ao consultar caixa", 500); return }
			totals, err := cashPaymentTotals(tx, id)
			if err != nil { http.Error(w, "Erro ao calcular pagamentos do caixa", 500); return }
			received := totals["cash"]
			expected := opening + received
			_, err = tx.Exec(`UPDATE cash_sessions SET closing_amount_cents=?, closed_by=?, closed_at=CURRENT_TIMESTAMP, status='closed' WHERE id=? AND status='open'`, req.Amount, nullableUserID(userID), id)
			if err != nil { http.Error(w, "Não foi possível fechar caixa", 500); return }
			if err = tx.Commit(); err != nil { http.Error(w, "Erro ao confirmar fechamento", 500); return }
			a.writeAudit(userID,"cash_closed","cash_session",strconv.FormatInt(id,10),fmt.Sprintf("Caixa fechado: esperado %d, contado %d, diferença %d centavos",expected,req.Amount,req.Amount-expected))
			jsonOut(w, 200, map[string]any{"open": false, "id": id, "opening_amount_cents": opening, "cash_received_cents": received, "pix_received_cents": totals["pix"], "debit_received_cents": totals["debit"], "credit_received_cents": totals["credit"], "voucher_received_cents": totals["voucher"], "total_sales_received_cents": totals["cash"]+totals["pix"]+totals["debit"]+totals["credit"]+totals["voucher"], "expected_cash_cents": expected, "counted_cash_cents": req.Amount, "difference_cents": req.Amount - expected})
		default: http.Error(w, "Ação inválida", 400)
		}
	default:
		w.Header().Set("Allow", "GET, POST"); http.Error(w, "Método não permitido", 405)
	}
}

 
// cashPaymentTotals keeps electronic payments out of the physical cash drawer calculation.
func cashPaymentTotals(db interface { QueryRow(query string, args ...any) *sql.Row }, sessionID int64) (map[string]int64, error) {
	var cash, pix, debit, credit, voucher int64
	err := db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN p.method='cash' THEN p.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN p.method='pix' THEN p.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN p.method='debit' THEN p.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN p.method='credit' THEN p.amount_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN p.method='voucher' THEN p.amount_cents ELSE 0 END),0)
	FROM payments p JOIN sales s ON s.id=p.sale_id
	WHERE s.cash_session_id=? AND s.status='completed'`, sessionID).Scan(&cash,&pix,&debit,&credit,&voucher)
	if err != nil { return nil, err }
	return map[string]int64{"cash":cash,"pix":pix,"debit":debit,"credit":credit,"voucher":voucher},nil
}
