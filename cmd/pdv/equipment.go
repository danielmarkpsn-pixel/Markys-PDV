package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type equipmentRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Model string `json:"model"`
	ConnectionType string `json:"connection_type"`
	ConnectionDetails string `json:"connection_details"`
	Protocol string `json:"protocol"`
	Status string `json:"status"`
	Notes string `json:"notes"`
}


func ensureEquipmentTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS equipment(
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL CHECK(kind IN('scale','barcode_scanner','card_terminal')),
		name TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		connection_type TEXT NOT NULL CHECK(connection_type IN('usb','serial','network','manual')),
		connection_details TEXT NOT NULL DEFAULT '',
		protocol TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN('pending','configured','inactive')),
		notes TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil { return err }
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_equipment_kind ON equipment(kind,status)`)
	return err
}

func (a *App) equipment(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil || u.Role != "admin" { http.Error(w, "Acesso negado", http.StatusForbidden); return }
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`SELECT id,kind,name,model,connection_type,connection_details,protocol,status,notes,created_at,updated_at FROM equipment ORDER BY id DESC`)
		if err != nil { http.Error(w, "Não foi possível consultar equipamentos", 500); return }
		defer rows.Close()
		type item struct {
			ID int64 `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
			Model string `json:"model"`
			ConnectionType string `json:"connection_type"`
			ConnectionDetails string `json:"connection_details"`
			Protocol string `json:"protocol"`
			Status string `json:"status"`
			Notes string `json:"notes"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		}
		out := []item{}
		for rows.Next() {
			var v item
			if err := rows.Scan(&v.ID,&v.Kind,&v.Name,&v.Model,&v.ConnectionType,&v.ConnectionDetails,&v.Protocol,&v.Status,&v.Notes,&v.CreatedAt,&v.UpdatedAt); err != nil { http.Error(w,"Erro ao ler equipamentos",500); return }
			out = append(out,v)
		}
		if err := rows.Err(); err != nil { http.Error(w,"Erro ao ler equipamentos",500); return }
		jsonOut(w,200,out)
	case http.MethodPost, http.MethodPut:
		r.Body = http.MaxBytesReader(w,r.Body,1<<20)
		var req equipmentRequest
		dec := json.NewDecoder(r.Body); dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil { http.Error(w,"Dados do equipamento inválidos",400); return }
		req.Kind = strings.TrimSpace(req.Kind); req.Name = strings.TrimSpace(req.Name)
		req.Model = strings.TrimSpace(req.Model); req.ConnectionDetails = strings.TrimSpace(req.ConnectionDetails)
		req.Protocol = strings.TrimSpace(req.Protocol); req.Notes = strings.TrimSpace(req.Notes)
		if req.Name == "" || len(req.Name)>120 { http.Error(w,"Informe um nome de equipamento com até 120 caracteres",400); return }
		if req.Kind!="scale" && req.Kind!="barcode_scanner" && req.Kind!="card_terminal" { http.Error(w,"Tipo de equipamento inválido",400); return }
		if req.ConnectionType!="usb" && req.ConnectionType!="serial" && req.ConnectionType!="network" && req.ConnectionType!="manual" { http.Error(w,"Tipo de conexão inválido",400); return }
		if req.Status=="" { req.Status="pending" }
		if req.Status!="pending" && req.Status!="configured" && req.Status!="inactive" { http.Error(w,"Situação inválida",400); return }
		if len(req.Model)>120 || len(req.ConnectionDetails)>200 || len(req.Protocol)>120 || len(req.Notes)>1000 { http.Error(w,"Um dos campos excedeu o limite permitido",400); return }
		user := a.currentUser(r)
		if r.Method==http.MethodPost {
			res,err:=a.db.Exec(`INSERT INTO equipment(kind,name,model,connection_type,connection_details,protocol,status,notes) VALUES(?,?,?,?,?,?,?,?)`,req.Kind,req.Name,req.Model,req.ConnectionType,req.ConnectionDetails,req.Protocol,req.Status,req.Notes)
			if err!=nil { http.Error(w,"Não foi possível cadastrar equipamento",500); return }
			id,_:=res.LastInsertId()
			if user!=nil { a.writeAudit(user.ID,"equipment_created","equipment",strconv.FormatInt(id,10),"Cadastrado equipamento: "+req.Name) }
			jsonOut(w,201,map[string]any{"id":id,"ok":true})
			return
		}
		id,err:=strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")),10,64)
		if err!=nil || id<=0 { http.Error(w,"Identificador de equipamento inválido",400); return }
		res,err:=a.db.Exec(`UPDATE equipment SET kind=?,name=?,model=?,connection_type=?,connection_details=?,protocol=?,status=?,notes=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,req.Kind,req.Name,req.Model,req.ConnectionType,req.ConnectionDetails,req.Protocol,req.Status,req.Notes,id)
		if err!=nil { http.Error(w,"Não foi possível atualizar equipamento",500); return }
		n,_:=res.RowsAffected(); if n==0 { http.Error(w,"Equipamento não encontrado",404); return }
		if user!=nil { a.writeAudit(user.ID,"equipment_updated","equipment",strconv.FormatInt(id,10),"Atualizado equipamento: "+req.Name) }
		jsonOut(w,200,map[string]any{"id":id,"ok":true})
	case http.MethodDelete:
		id,err:=strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")),10,64)
		if err!=nil || id<=0 { http.Error(w,"Identificador de equipamento inválido",400); return }
		res,err:=a.db.Exec("DELETE FROM equipment WHERE id=?",id)
		if err!=nil { http.Error(w,"Não foi possível excluir equipamento",500); return }
		n,_:=res.RowsAffected(); if n==0 { http.Error(w,"Equipamento não encontrado",404); return }
		jsonOut(w,200,map[string]any{"ok":true})
	default:
		w.Header().Set("Allow","GET, POST, PUT, DELETE"); http.Error(w,"Método não permitido",405)
	}
}
