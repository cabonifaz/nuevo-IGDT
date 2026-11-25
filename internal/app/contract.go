package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Fin de contrato (V0021). Cuando el contrato vigente de un ingreso está por terminar se alerta a sus gestores (los del
// cliente actual, el gestor dirigido o, si el cliente no tiene gestores, los administradores) con niveles según los días
// de aviso del tenant (por defecto 30, 15 y 7) y el asistente guía la operación:
//   - RENEW (renovar): nueva fecha de fin; se reenvía la FMI actualizada.                         Evento RENEWED.
//   - TERMINATE (baja): fecha y motivo de cese y si se recupera el equipo; 3 correos: FMI de baja (sección CESE con
//     «Recuperar laptop/equipo»), baja de credenciales (FT-GS-01 desactivación) y recuperación de equipo.  TERMINATED.
//   - TRANSFER (traspaso): otro cliente, área, puesto y jornada (sección MOVIMIENTO); opcional modificar credenciales.
//     Los gestores del nuevo cliente pasan a verlo.                                                 TRANSFERRED.
//   - RQ_CHANGE (cambio de RQ): pasa a un RQ abierto de BDT; se cierra el contrato (FMI de salida) y se avisa a Selección
//     que gestione el ingreso en BDT.                                                        RQ_CHANGE_REQUESTED.

// DefaultContractAlertDays: días de anticipación de los avisos por defecto.
var DefaultContractAlertDays = []int{30, 15, 7}

// normalizeAlertDays: de 1 a 5 valores entre 1 y 120, sin repetir, de mayor a menor (vacío = por defecto).
func normalizeAlertDays(in []int) ([]int, error) {
	if len(in) == 0 {
		return append([]int{}, DefaultContractAlertDays...), nil
	}
	seen, out := map[int]bool{}, []int{}
	for _, d := range in {
		if d < 1 || d > 120 {
			return nil, domain.ErrValidation
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) > 5 {
		return nil, domain.ErrValidation
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out, nil
}

// Niveles de aviso de un contrato por terminar.
const (
	LevelNotice   = "NOTICE"   // dentro del mayor umbral (p. ej. ≤ 30 días)
	LevelReminder = "REMINDER" // umbrales intermedios (≤ 15)
	LevelCritical = "CRITICAL" // dentro del menor umbral (≤ 7)
	LevelExpired  = "EXPIRED"  // ya pasó la fecha de fin
	LevelOK       = "OK"
)

// contractLevel: nivel según los días que faltan para el fin (days: umbrales del tenant), los días y el umbral alcanzado.
// Con un solo umbral solo hay aviso (y vencido); con dos, aviso y crítico.
func contractLevel(endDate string, today time.Time, days []int) (level string, left, threshold int) {
	d, err := time.ParseInLocation("2006-01-02", dateOnly(endDate), today.Location())
	if err != nil {
		return LevelOK, 0, 0
	}
	t := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	left = int(math.Round(d.Sub(t).Hours() / 24))
	if left < 0 {
		return LevelExpired, left, 0
	}
	asc := append([]int{}, days...)
	sort.Ints(asc)
	for i, th := range asc {
		if left <= th {
			switch {
			case i == len(asc)-1:
				level = LevelNotice
			case i == 0:
				level = LevelCritical
			default:
				level = LevelReminder
			}
			return level, left, th
		}
	}
	return LevelOK, left, 0
}

var levelRank = map[string]int{LevelExpired: 0, LevelCritical: 1, LevelReminder: 2, LevelNotice: 3, LevelOK: 4}

// ContractAlertItem: contrato vigente por terminar que el usuario puede gestionar.
type ContractAlertItem struct {
	RequestID  string `json:"requestId"`
	HireID     string `json:"hireId"`
	EmployeeID string `json:"employeeId,omitempty"`
	Talent     string `json:"talent"`
	Client     string `json:"client"`
	RQ         string `json:"rq"`
	Position   string `json:"position"`
	StartDate  string `json:"startDate"`
	EndDate    string `json:"endDate"`
	DaysLeft   int    `json:"daysLeft"`
	Level      string `json:"level"`
	Threshold  int    `json:"threshold,omitempty"`
}

// ContractAlerts: resumen para el banner, la notificación diaria y el correo de recordatorio.
type ContractAlerts struct {
	Total    int                 `json:"total"`
	Notice   int                 `json:"notice"`
	Reminder int                 `json:"reminder"`
	Critical int                 `json:"critical"`
	Expired  int                 `json:"expired"`
	Days     []int               `json:"days"`
	Items    []ContractAlertItem `json:"items"`
}

func (a *ContractAlerts) add(it ContractAlertItem) {
	a.Total++
	switch it.Level {
	case LevelNotice:
		a.Notice++
	case LevelReminder:
		a.Reminder++
	case LevelCritical:
		a.Critical++
	case LevelExpired:
		a.Expired++
	}
	a.Items = append(a.Items, it)
}

func sortContractItems(items []ContractAlertItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].EndDate != items[j].EndDate {
			return items[i].EndDate < items[j].EndDate
		}
		return items[i].Talent < items[j].Talent
	})
}

// contractRow: fila de sp_contract_alerts / sp_contract_ending.
type contractRow struct {
	RequestID  string `json:"requestId"`
	HireID     string `json:"hireId"`
	EmployeeID string `json:"employeeId"`
	Talent     struct {
		FirstName      string `json:"firstName"`
		LastName       string `json:"lastName"`
		SecondLastName string `json:"secondLastName"`
	} `json:"talent"`
	Client    string         `json:"client"`
	RQ        string         `json:"rq"`
	Position  string         `json:"position"`
	StartDate string         `json:"startDate"`
	EndDate   string         `json:"endDate"`
	Managers  []reminderUser `json:"managers"`
}

func (r contractRow) item(today time.Time, days []int) ContractAlertItem {
	lv, left, th := contractLevel(r.EndDate, today, days)
	return ContractAlertItem{RequestID: r.RequestID, HireID: r.HireID, EmployeeID: r.EmployeeID,
		Talent: strings.Join(nonEmpty([]string{r.Talent.FirstName, r.Talent.LastName, r.Talent.SecondLastName}), " "),
		Client: r.Client, RQ: r.RQ, Position: r.Position, StartDate: dateOnly(r.StartDate), EndDate: dateOnly(r.EndDate),
		DaysLeft: left, Level: lv, Threshold: th}
}

// ContractAlerts: contratos por terminar (o vencidos) que el usuario puede gestionar.
func (s *SelectionService) ContractAlerts(ctx context.Context, p *domain.Principal) (*ContractAlerts, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	st, err := s.settings(ctx, tid)
	if err != nil {
		return nil, err
	}
	today := time.Now().In(limaTZ())
	docs, err := s.Repo.ContractAlerts(ctx, meta(p, ""), tid, today, st.ContractAlertDays[0])
	if err != nil {
		return nil, err
	}
	out := &ContractAlerts{Days: st.ContractAlertDays, Items: []ContractAlertItem{}}
	for _, d := range docs {
		var r contractRow
		if json.Unmarshal(d, &r) != nil {
			continue
		}
		if it := r.item(today, st.ContractAlertDays); it.Level != LevelOK {
			out.add(it)
		}
	}
	sortContractItems(out.Items)
	return out, nil
}

// ---------------- Operaciones ----------------

// Tipos de operación de fin de contrato.
const (
	OpRenew     = "RENEW"
	OpTerminate = "TERMINATE"
	OpTransfer  = "TRANSFER"
	OpRQChange  = "RQ_CHANGE"
)

var opReason = map[string]string{OpRenew: "RENEWED", OpTerminate: "TERMINATED", OpTransfer: "TRANSFERRED", OpRQChange: "RQ_CHANGE"}

// ContractOpInput: lo indicado en el asistente. Date: cese (baja), movimiento (traspaso) o salida (cambio de RQ).
// EndDate: nueva fecha de fin (renovar; en traspaso, salvo Indefinite).
type ContractOpInput struct {
	Type                string `json:"type"`
	Date                string `json:"date,omitempty"`
	EndDate             string `json:"endDate,omitempty"`
	Indefinite          bool   `json:"indefinite,omitempty"`
	Reason              string `json:"reason,omitempty"`
	TerminationReasonID string `json:"terminationReasonId,omitempty"`
	RecoverEquipment    bool   `json:"recoverEquipment,omitempty"`
	EquipmentReturnDate string `json:"equipmentReturnDate,omitempty"`
	ClientID            string `json:"clientId,omitempty"`
	AreaID              string `json:"areaId,omitempty"`
	Position            string `json:"position,omitempty"`
	Schedule            string `json:"schedule,omitempty"`
	ModifyCredentials   bool   `json:"modifyCredentials,omitempty"`
	NewUsername         string `json:"newUsername,omitempty"`
	NewEmail            string `json:"newEmail,omitempty"`
	RequirementID       string `json:"requirementId,omitempty"`
}

func reqDate(v string) bool { return v != "" && validDate(v) }

// validate revisa la operación contra el contrato vigente (inicio y fin actuales) y deja solo lo que aplica al tipo.
func (in *ContractOpInput) validate(start, end string) error {
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.Reason, in.Position, in.Schedule = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Position), strings.TrimSpace(in.Schedule)
	in.NewUsername, in.NewEmail = strings.TrimSpace(in.NewUsername), strings.ToLower(strings.TrimSpace(in.NewEmail))
	start, end = dateOnly(start), dateOnly(end)
	if !maxLen(500, in.Reason) || !maxLen(150, in.Position) || !maxLen(200, in.Schedule) || !validDate(in.Date) || !validDate(in.EndDate) ||
		!validDate(in.EquipmentReturnDate) {
		return domain.ErrValidation
	}
	afterStart := func(d string) error {
		if !reqDate(d) || d < start {
			return domain.NewError("CONTRACT_DATE_INVALID")
		}
		return nil
	}
	switch in.Type {
	case OpRenew:
		if !reqDate(in.EndDate) || in.EndDate < start || (end != "" && in.EndDate <= end) {
			return domain.NewError("CONTRACT_DATE_INVALID")
		}
		if in.Reason == "" {
			return domain.NewError("REASON_REQUIRED")
		}
		*in = ContractOpInput{Type: in.Type, EndDate: in.EndDate, Reason: in.Reason}
	case OpTerminate:
		if err := afterStart(in.Date); err != nil {
			return err
		}
		if !validUUID(in.TerminationReasonID) {
			return domain.ErrValidation
		}
		if in.RecoverEquipment {
			if err := afterStart(in.EquipmentReturnDate); err != nil {
				return err
			}
		} else {
			in.EquipmentReturnDate = ""
		}
		*in = ContractOpInput{Type: in.Type, Date: in.Date, Reason: in.Reason, TerminationReasonID: in.TerminationReasonID,
			RecoverEquipment: in.RecoverEquipment, EquipmentReturnDate: in.EquipmentReturnDate}
	case OpTransfer:
		if !validUUID(in.ClientID) || !validUUID(in.AreaID) || in.Position == "" || in.Schedule == "" {
			return domain.ErrValidation
		}
		if err := afterStart(in.Date); err != nil {
			return err
		}
		if in.Indefinite {
			in.EndDate = ""
		} else if !reqDate(in.EndDate) || in.EndDate <= in.Date {
			return domain.NewError("CONTRACT_DATE_INVALID")
		}
		if in.ModifyCredentials {
			if in.NewUsername == "" && in.NewEmail == "" {
				return domain.ErrValidation
			}
			if in.NewUsername != "" && !usernameRe.MatchString(in.NewUsername) {
				return domain.ErrValidation
			}
			if in.NewEmail != "" {
				if a, err := mail.ParseAddress(in.NewEmail); err != nil || a.Address != in.NewEmail || len(in.NewEmail) > 150 {
					return domain.ErrValidation
				}
			}
		} else {
			in.NewUsername, in.NewEmail = "", ""
		}
		*in = ContractOpInput{Type: in.Type, Date: in.Date, EndDate: in.EndDate, Indefinite: in.Indefinite, Reason: in.Reason,
			ClientID: in.ClientID, AreaID: in.AreaID, Position: in.Position, Schedule: in.Schedule,
			ModifyCredentials: in.ModifyCredentials, NewUsername: in.NewUsername, NewEmail: in.NewEmail}
	case OpRQChange:
		if !validUUID(in.RequirementID) {
			return domain.ErrValidation
		}
		if err := afterStart(in.Date); err != nil {
			return err
		}
		*in = ContractOpInput{Type: in.Type, Date: in.Date, Reason: in.Reason, RequirementID: in.RequirementID}
	default:
		return domain.ErrValidation
	}
	return nil
}

// contractDoc: contexto del contrato (sp_hire_contract_get).
type contractDoc struct {
	HireID     string `json:"hireId"`
	RequestID  string `json:"requestId"`
	EmployeeID string `json:"employeeId"`
	Status     string `json:"status"`
	Talent     struct {
		FirstName      string `json:"firstName"`
		LastName       string `json:"lastName"`
		SecondLastName string `json:"secondLastName"`
	} `json:"talent"`
	Client struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"client"`
	Requirement   string `json:"requirement"`
	RequirementID string `json:"requirementId"`
	Position      string `json:"position"`
	Area          struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"area"`
	Schedule  string       `json:"schedule"`
	StartDate string       `json:"startDate"`
	EndDate   string       `json:"endDate"`
	Users     *UserRequest `json:"users"`
	Equipment []struct {
		Label       string            `json:"label"`
		Status      string            `json:"status"`
		DeliveredAt string            `json:"deliveredAt"`
		Details     map[string]string `json:"details"`
	} `json:"equipment"`
}

func (s *SelectionService) contractDoc(ctx context.Context, p *domain.Principal, tid uint64, requestID string) (*contractDoc, json.RawMessage, error) {
	raw, err := s.Repo.HireContract(ctx, meta(p, ""), tid, requestID)
	if err != nil {
		return nil, nil, err
	}
	var d contractDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, nil, err
	}
	return &d, raw, nil
}

// contractSnapshot: el contrato antes de la operación (para imprimir la FMI con el ingreso original y el movimiento).
type contractSnapshot struct {
	ClientID  string `json:"clientId,omitempty"`
	Client    string `json:"client"`
	AreaID    string `json:"areaId,omitempty"`
	Area      string `json:"area"`
	Position  string `json:"position"`
	Schedule  string `json:"schedule"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate,omitempty"`
	RQ        string `json:"rq,omitempty"`
}

type opLabels struct {
	Client            string `json:"client,omitempty"`
	Area              string `json:"area,omitempty"`
	TerminationReason string `json:"terminationReason,omitempty"`
	RQCode            string `json:"rqCode,omitempty"`
	RQTitle           string `json:"rqTitle,omitempty"`
	RQClient          string `json:"rqClient,omitempty"`
}

// equipItem: equipo a recuperar (de la ficha de equipo aprobada).
type equipItem struct {
	Label       string `json:"label"`
	Detail      string `json:"detail,omitempty"`
	DeliveredAt string `json:"deliveredAt,omitempty"`
}

// contractOpData: lo que se guarda de la operación (hire_contract_op.data); con esto se reimprimen sus documentos.
type contractOpData struct {
	Type      string           `json:"type"`
	Input     ContractOpInput  `json:"input"`
	Before    contractSnapshot `json:"before"`
	Labels    opLabels         `json:"labels"`
	Equipment []equipItem      `json:"equipment,omitempty"`
	Users     *UserRequest     `json:"users,omitempty"`
}

func equipDetail(d map[string]string) string {
	parts := []string{}
	for _, k := range []string{"brand", "processor", "ram", "disk", "accessories"} {
		if v := strings.TrimSpace(d[k]); v != "" && v != "null" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " · ")
}

// buildOpData valida contra los catálogos y BDT y arma lo que se guarda de la operación.
func (s *SelectionService) buildOpData(ctx context.Context, p *domain.Principal, tid uint64, cd *contractDoc, in ContractOpInput) (*contractOpData, error) {
	op := &contractOpData{Type: in.Type, Input: in, Users: cd.Users,
		Before: contractSnapshot{ClientID: cd.Client.ID, Client: cd.Client.Label, AreaID: cd.Area.ID, Area: cd.Area.Label, Position: cd.Position,
			Schedule: cd.Schedule, StartDate: dateOnly(cd.StartDate), EndDate: dateOnly(cd.EndDate), RQ: cd.Requirement}}
	ids := nonEmpty([]string{in.ClientID, in.AreaID, in.TerminationReasonID})
	if len(ids) > 0 {
		labels, err := s.Repo.CatalogLabels(ctx, tid, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if labels[id].Label == "" {
				return nil, domain.ErrValidation
			}
		}
		op.Labels.Client, op.Labels.Area, op.Labels.TerminationReason = labels[in.ClientID].Label, labels[in.AreaID].Label, labels[in.TerminationReasonID].Label
	}
	switch in.Type {
	case OpTerminate:
		if in.RecoverEquipment {
			for _, e := range cd.Equipment {
				if e.Status == "DELIVERED" || e.Status == "PENDING_RETURN" {
					op.Equipment = append(op.Equipment, equipItem{Label: e.Label, Detail: equipDetail(e.Details), DeliveredAt: dateOnly(e.DeliveredAt)})
				}
			}
		}
	case OpRQChange:
		if in.RequirementID == cd.RequirementID {
			return nil, domain.ErrValidation
		}
		raw, err := s.Repo.Requirement(ctx, meta(p, ""), tid, in.RequirementID)
		if err != nil {
			return nil, err
		}
		var rq struct {
			Code   string `json:"code"`
			Title  string `json:"title"`
			Status struct {
				Group string `json:"group"`
			} `json:"status"`
			Client struct {
				Label string `json:"label"`
			} `json:"client"`
		}
		if json.Unmarshal(raw, &rq) != nil || rq.Status.Group != "OPEN" {
			return nil, domain.NewError("RQ_NOT_OPEN")
		}
		op.Labels.RQCode, op.Labels.RQTitle, op.Labels.RQClient = rq.Code, rq.Title, rq.Client.Label
	}
	return op, nil
}

// changes: lo que cambia en el contrato, para el historial del ingreso.
func (op *contractOpData) changes() []FieldChange {
	in, b := op.Input, op.Before
	out := []FieldChange{}
	add := func(field, from, to string) {
		if from != to {
			out = append(out, FieldChange{field, from, to})
		}
	}
	switch op.Type {
	case OpRenew:
		add("endDate", b.EndDate, in.EndDate)
	case OpTerminate:
		add("terminationDate", "", in.Date)
		add("terminationReason", "", op.Labels.TerminationReason)
		if in.RecoverEquipment {
			add("equipmentReturnDate", "", in.EquipmentReturnDate)
		}
	case OpTransfer:
		add("client", b.Client, op.Labels.Client)
		add("area", b.Area, op.Labels.Area)
		add("position", b.Position, in.Position)
		add("schedule", b.Schedule, in.Schedule)
		add("endDate", b.EndDate, in.EndDate)
		add("movementDate", "", in.Date)
		if in.ModifyCredentials {
			add("userRequest", "", strings.Join(nonEmpty([]string{in.NewUsername, in.NewEmail}), " · "))
		}
	case OpRQChange:
		add("requirement", b.RQ, op.Labels.RQCode)
		add("exitDate", "", in.Date)
	}
	return out
}

// opForms: documentos que se envían en cada operación.
func opForms(op *contractOpData) []string {
	switch op.Type {
	case OpTerminate:
		if op.Input.RecoverEquipment {
			return []string{"FMI", "USERS", "RECOVERY"}
		}
		return []string{"FMI", "USERS"}
	case OpTransfer:
		if op.Input.ModifyCredentials {
			return []string{"FMI", "USERS"}
		}
	case OpRQChange:
		return []string{"FMI", "NOTICE"}
	}
	return []string{"FMI"}
}

// ContractOpResult: operación registrada y los correos que se envían (en segundo plano).
type ContractOpResult struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Documents []string `json:"documents"`
}

func (s *SelectionService) prepareOp(ctx context.Context, p *domain.Principal, tid uint64, requestID string, in ContractOpInput) (*contractDoc, *contractOpData, error) {
	if !validUUID(requestID) {
		return nil, nil, domain.ErrValidation
	}
	cd, _, err := s.contractDoc(ctx, p, tid, requestID)
	if err != nil {
		return nil, nil, err
	}
	if cd.Status != "ACTIVE" {
		return nil, nil, domain.NewError("CONTRACT_CLOSED")
	}
	if err := in.validate(cd.StartDate, cd.EndDate); err != nil {
		return nil, nil, err
	}
	op, err := s.buildOpData(ctx, p, tid, cd, in)
	if err != nil {
		return nil, nil, err
	}
	return cd, op, nil
}

// ContractOp ejecuta la operación de fin de contrato y envía sus correos.
func (s *SelectionService) ContractOp(ctx context.Context, p *domain.Principal, ip, requestID string, in ContractOpInput) (*ContractOpResult, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	_, op, err := s.prepareOp(ctx, p, tid, requestID, in)
	if err != nil {
		return nil, err
	}
	in = op.Input
	date := in.Date
	if in.Type == OpRenew {
		date = time.Now().In(limaTZ()).Format("2006-01-02")
	}
	data, _ := json.Marshal(op)
	changes, _ := json.Marshal(op.changes())
	id, err := s.Repo.HireContractOp(ctx, meta(p, ip), tid, ports.ContractOpWrite{RequestID: requestID, OpID: uuid.NewString(), Type: in.Type,
		Date: date, EndDate: in.EndDate, TerminationReasonID: in.TerminationReasonID, Recover: in.RecoverEquipment, ReturnDate: in.EquipmentReturnDate,
		ClientID: in.ClientID, AreaID: in.AreaID, Position: in.Position, Schedule: in.Schedule, RQID: in.RequirementID, Reason: in.Reason,
		Data: data, Changes: changes})
	if err != nil {
		return nil, err
	}
	s.contractDispatchAsync(p, tid, requestID, id, op, opReason[op.Type])
	return &ContractOpResult{ID: id, Type: op.Type, Documents: opForms(op)}, nil
}

// ContractPreview: PDF (FMI o USERS) de la operación antes de confirmarla.
func (s *SelectionService) ContractPreview(ctx context.Context, p *domain.Principal, requestID, form string, in ContractOpInput) ([]byte, string, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, "", err
	}
	if (form != "FMI" && form != "USERS") || s.Renderer == nil {
		return nil, "", domain.ErrValidation
	}
	_, op, err := s.prepareOp(ctx, p, tid, requestID, in)
	if err != nil {
		return nil, "", err
	}
	c, err := s.contractContext(ctx, p, tid, requestID, op, false)
	if err != nil {
		return nil, "", err
	}
	b, err := s.Renderer.Render(contractPrint(form, c, op))
	if err != nil {
		return nil, "", err
	}
	return b, fileName(form, c.talentName()), nil
}

// ResendContractOp reenvía los documentos de una operación ya registrada.
func (s *SelectionService) ResendContractOp(ctx context.Context, p *domain.Principal, requestID, opID string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(requestID) || !validUUID(opID) {
		return domain.ErrValidation
	}
	if err := s.Repo.HireContractAssertManager(ctx, meta(p, ""), tid, requestID); err != nil {
		return err
	}
	raw, err := s.Repo.HireContractOpGet(ctx, meta(p, ""), tid, requestID, opID)
	if err != nil {
		return err
	}
	var x struct {
		Data contractOpData `json:"data"`
	}
	if err := json.Unmarshal(raw, &x); err != nil || x.Data.Type == "" {
		return domain.ErrNotFound
	}
	s.contractDispatchAsync(p, tid, requestID, opID, &x.Data, "RESENT")
	return nil
}

// Contract: contexto del asistente: contrato, nivel de aviso, operaciones, equipo y a quién va cada correo.
func (s *SelectionService) Contract(ctx context.Context, p *domain.Principal, requestID string) (json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(requestID) {
		return nil, domain.ErrValidation
	}
	cd, raw, err := s.contractDoc(ctx, p, tid, requestID)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(decryptNames(s.Cipher, raw), &m); err != nil {
		return nil, err
	}
	st, err := s.settings(ctx, tid)
	if err != nil {
		return nil, err
	}
	m["alertDays"] = st.ContractAlertDays
	m["mailer"] = s.Mail != nil && mailerEnabled(s.Mail)
	if cd.Status == "ACTIVE" && cd.EndDate != "" {
		lv, left, th := contractLevel(cd.EndDate, time.Now().In(limaTZ()), st.ContractAlertDays)
		m["level"], m["daysLeft"], m["threshold"] = lv, left, th
	}
	if c, err := s.formContextFor(ctx, p, tid, requestID, "FMI", false); err == nil {
		plan := map[string]map[string][]string{}
		for _, f := range []string{"FMI", "USERS", "RECOVERY", "NOTICE"} {
			to, cc, _ := contractRecipients(f, c, p.Email)
			plan[f] = map[string][]string{"to": to, "cc": cc}
		}
		m["recipients"] = plan
	}
	return json.Marshal(m)
}

// ---------------- Documentos y envío ----------------

// contractContext: contexto de impresión del contrato con la foto anterior a la operación, firmado por quien la ejecuta.
func (s *SelectionService) contractContext(ctx context.Context, p *domain.Principal, tid uint64, requestID string, op *contractOpData, internal bool) (*formContext, error) {
	c, err := s.formContextFor(ctx, p, tid, requestID, "FMI", internal)
	if err != nil {
		return nil, err
	}
	if p.FullName != "" {
		c.manager = p.FullName
	}
	if sig, err := s.signature(ctx, tid, p.UserID, ""); err == nil {
		c.signature = sig
	}
	c.applyOp(op)
	return c, nil
}

// applyOp deja en el contexto el contrato tal como estaba antes de la operación (en la renovación, con el nuevo fin).
func (c *formContext) applyOp(op *contractOpData) {
	b := op.Before
	if b.Client != "" {
		c.req["client"] = map[string]any{"id": b.ClientID, "label": b.Client}
	}
	h := &c.forms.Hire
	if b.Position != "" {
		h.Position, h.Schedule = b.Position, b.Schedule
	}
	if b.AreaID != "" {
		h.AreaID = b.AreaID
		if c.labels == nil {
			c.labels = map[string]ports.CatalogLabel{}
		}
		c.labels[b.AreaID] = ports.CatalogLabel{Label: b.Area}
	}
	if b.StartDate != "" {
		h.StartDate = b.StartDate
	}
	h.EndDate = b.EndDate
	if op.Type == OpRenew {
		h.EndDate = op.Input.EndDate
	}
	if op.Users != nil {
		c.forms.Users = op.Users
	}
}

// recoverText: indicación de la FMI de baja («Recuperar laptop/equipo: …»).
func recoverText(op *contractOpData) string {
	if !op.Input.RecoverEquipment {
		return ""
	}
	items := []string{}
	for _, e := range op.Equipment {
		if e.Detail != "" {
			items = append(items, fmt.Sprintf("%s (%s)", e.Label, e.Detail))
		} else {
			items = append(items, e.Label)
		}
	}
	what := "Sí, el equipo asignado al colaborador"
	if len(items) > 0 {
		what = "Sí: " + strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s. Devolución: %s.", what, fmtDate(op.Input.EquipmentReturnDate))
}

// contractPrint: la FMI (FT-GTH-12 con MOVIMIENTO o CESE) o la ficha de usuarios (FT-GS-01 modificación o desactivación).
func contractPrint(form string, c *formContext, op *contractOpData) ports.FormPrint {
	pr := c.build(form)
	in := op.Input
	note := func(f *ports.FMIPrint, text string) {
		f.Notes = strings.TrimSpace(strings.Join(nonEmpty([]string{f.Notes, text}), "\n"))
	}
	if f := pr.FMI; f != nil {
		f.Compact = true
		switch op.Type {
		case OpRenew:
			note(f, fmt.Sprintf("Renovación de contrato: nuevo término %s (término anterior: %s). Motivo: %s.", fmtDate(in.EndDate),
				orDash(fmtDate(op.Before.EndDate)), in.Reason))
		case OpTerminate:
			f.TermReason, f.TermDate = op.Labels.TerminationReason, fmtDate(in.Date)
			if in.RecoverEquipment {
				f.ReturnDate, f.Recover = fmtDate(in.EquipmentReturnDate), recoverText(op)
			}
			if in.Reason != "" {
				note(f, "Baja: "+in.Reason)
			}
		case OpTransfer:
			if op.Labels.Client != op.Before.Client {
				f.MoveClient = op.Labels.Client
			}
			if in.Position != op.Before.Position {
				f.MovePosition = in.Position
			}
			if op.Labels.Area != op.Before.Area {
				f.MoveArea = op.Labels.Area
			}
			if in.Schedule != op.Before.Schedule {
				f.MoveSchedule = in.Schedule
			}
			f.MoveDate = fmtDate(in.Date)
			end := "Indeterminado"
			if in.EndDate != "" {
				end = fmtDate(in.EndDate)
			}
			note(f, fmt.Sprintf("Traspaso a %s desde el %s. Nuevo término de contrato: %s.", op.Labels.Client, fmtDate(in.Date), end))
		case OpRQChange:
			f.TermReason = strings.TrimSpace(fmt.Sprintf("Cambio a otro requerimiento: %s %s", op.Labels.RQCode, dashed(op.Labels.RQTitle)))
			f.TermDate, f.MoveDate = fmtDate(in.Date), fmtDate(in.Date)
			if op.Labels.RQClient != "" && op.Labels.RQClient != op.Before.Client {
				f.MoveClient = op.Labels.RQClient
			}
		}
	}
	if u := pr.Users; u != nil {
		name, user, email := u.Name, u.Username, u.Email
		u.Name, u.Username, u.Email, u.Area, u.Notes = "", "", "", "", ""
		switch op.Type {
		case OpTerminate:
			u.DeactName, u.DeactUser, u.DeactEmail = name, user, email
			u.DeactReason = strings.TrimSpace(fmt.Sprintf("Cese a partir del %s (%s)", fmtDate(in.Date), op.Labels.TerminationReason))
		case OpTransfer:
			u.ModUserOn, u.ModUser, u.ModUserTo = in.NewUsername != "", user, in.NewUsername
			u.ModEmailOn, u.ModEmail, u.ModEmailTo = in.NewEmail != "", email, in.NewEmail
		}
	}
	return pr
}

func orDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func dashed(v string) string {
	if v == "" {
		return ""
	}
	return "– " + v
}

// contractRecipients: FMI → solicitantes, cc Talento y copias fijas; USERS y RECOVERY → Soporte, cc solicitantes y copias
// fijas; NOTICE (cambio de RQ) → solicitantes (Selección), cc copias fijas. Más el gestor si ccManager.
func contractRecipients(form string, c *formContext, approverEmail string) (to, cc []string, greet string) {
	switch form {
	case "FMI":
		return (&SelectionService{}).recipientsFor("FMI", c, approverEmail)
	case "NOTICE":
		reqs := c.requesters()
		for _, r := range reqs {
			to = append(to, r.Email)
		}
		to = dedupe(to)
		cc = append([]string{}, c.settings.CCEmails...)
		if len(to) == 0 {
			to = dedupe(c.settings.TalentEmails)
		} else if n := strings.Fields(reqs[0].Name); len(n) > 0 {
			greet = n[0]
		}
		if c.settings.CCManager && approverEmail != "" {
			cc = append(cc, approverEmail)
		}
		return to, dedupe(cc, to), greet
	}
	return (&SelectionService{}).recipientsFor("USERS", c, approverEmail)
}

func (s *SelectionService) contractDispatchAsync(p *domain.Principal, tid uint64, requestID, opID string, op *contractOpData, reason string) {
	if s.Renderer == nil || s.Mail == nil {
		return
	}
	pc := *p
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := s.contractDispatch(ctx, &pc, tid, requestID, opID, op, reason); err != nil {
			slog.Warn("no se pudieron enviar los documentos del fin de contrato", "request", requestID, "op", opID, "err", err)
		}
	}()
}

// contractDispatch envía cada documento de la operación y registra el resultado (enlazado a la operación).
func (s *SelectionService) contractDispatch(ctx context.Context, p *domain.Principal, tid uint64, requestID, opID string, op *contractOpData, reason string) error {
	log := func(form string, rcpt []string, status, errText string) {
		_ = s.Repo.HireOpDispatchAdd(ctx, p.UserID, tid, requestID, opID, form, reason, rcpt, status, errText)
	}
	c, err := s.contractContext(ctx, p, tid, requestID, op, true)
	if err != nil {
		for _, f := range opForms(op) {
			log(f, nil, "FAILED", err.Error())
		}
		return err
	}
	for _, form := range opForms(op) {
		to, cc, greet := contractRecipients(form, c, p.Email)
		if len(to) == 0 {
			log(form, nil, "NO_RECIPIENTS", "")
			continue
		}
		all := append(append([]string{}, to...), cc...)
		msg := ports.Mail{To: to, Cc: cc}
		msg.Subject, msg.HTML, msg.Text = buildContractMail(form, reason, op, c, greet)
		if (form == "USERS" || form == "RECOVERY") && c.print != nil && len(c.print.logo) > 0 {
			msg.Attachments = append(msg.Attachments, ports.MailAttachment{Name: "logo.png", ContentType: "image/png", Data: c.print.logo, ContentID: "logo"})
		}
		if form == "FMI" || form == "USERS" {
			pdf, err := s.Renderer.Render(contractPrint(form, c, op))
			if err != nil {
				log(form, all, "FAILED", err.Error())
				continue
			}
			msg.Attachments = append(msg.Attachments, ports.MailAttachment{Name: attachName(form, c.header(form).Code, c.talentName()),
				ContentType: "application/pdf", Data: pdf})
		}
		status, errText := "SENT", ""
		if err := s.Mail.Send(ctx, msg); err != nil {
			status, errText = "FAILED", err.Error()
			if err == ports.ErrNoMailer {
				status, errText = "NO_MAILER", ""
			}
		}
		log(form, all, status, errText)
	}
	return nil
}
