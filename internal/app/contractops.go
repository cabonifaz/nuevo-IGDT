package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Operaciones de fin de contrato (v0.13, v0.14):
//   - RENEW (renovar): nueva fecha de fin; se reenvía la FMI actualizada.                         Evento RENEWED.
//   - TERMINATE (baja): fecha y motivo de cese y si se recupera el equipo. Con fecha de cese hoy o pasada se hace
//     efectiva al confirmar; con fecha futura queda PROGRAMADA (el contrato sigue vigente) y la rutina diaria la aplica en
//     esa fecha. Los 3 correos (FMI de baja, baja de credenciales y recuperación de equipo) salen al confirmar con la
//     fecha efectiva. Mientras está programada se puede cambiar la fecha o anular (con motivo; se avisa).  TERMINATED.
//   - TRANSFER (traspaso de área en el mismo cliente): área (distinta), cargo, jornada, sede, lugar de trabajo,
//     proyecto, fecha de fin, remuneración y observaciones; FMI con MOVIMIENTO. Opcional modificar credenciales.  TRANSFERRED.
//   - RQ_CHANGE (cambio de RQ): pasa a uno o más RQ abiertos de BDT; se cierra el contrato (FMI de salida) y se avisa a
//     Selección que gestione el ingreso en BDT.                                                RQ_CHANGE_REQUESTED.
// Los documentos de cada operación los firma quien la confirmó (o quien cambió la fecha de la baja programada).

// Tipos de operación de fin de contrato.
const (
	OpRenew     = "RENEW"
	OpTerminate = "TERMINATE"
	OpTransfer  = "TRANSFER" // compatibilidad: traspaso de área = movimiento con cambio de área
	OpRQChange  = "RQ_CHANGE"
	OpMovement  = "MOVEMENT"
)

var opReason = map[string]string{OpRenew: "RENEWED", OpTerminate: "TERMINATED", OpTransfer: "TRANSFERRED", OpRQChange: "RQ_CHANGE", OpMovement: "MOVED"}

// ContractOpInput: lo indicado en el asistente. Date: cese (baja), movimiento (traspaso) o salida (cambio de RQ).
// EndDate: nueva fecha de fin (renovar; en traspaso, salvo Indefinite). Pay: nueva remuneración del traspaso (nil = sin
// cambio; nunca se guarda en claro). RequirementID: compatibilidad (un solo RQ); RequirementIDs: los RQ destino.
type ContractOpInput struct {
	Type                string   `json:"type"`
	Date                string   `json:"date,omitempty"`
	EndDate             string   `json:"endDate,omitempty"`
	Indefinite          bool     `json:"indefinite,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	TerminationReasonID string   `json:"terminationReasonId,omitempty"`
	RecoverEquipment    bool     `json:"recoverEquipment,omitempty"`
	EquipmentReturnDate string   `json:"equipmentReturnDate,omitempty"`
	AreaID              string   `json:"areaId,omitempty"`
	Position            string   `json:"position,omitempty"`
	Schedule            string   `json:"schedule,omitempty"`
	SiteID              string   `json:"siteId,omitempty"`
	Location            string   `json:"location,omitempty"`
	ProjectService      string   `json:"projectService,omitempty"`
	CurrencyID          string   `json:"currencyId,omitempty"`
	Pay                 *Pay     `json:"pay,omitempty"`
	ChangeArea          bool     `json:"changeArea,omitempty"`
	ChangePosition      bool     `json:"changePosition,omitempty"`
	ChangeSchedule      bool     `json:"changeSchedule,omitempty"`
	ChangeWorkplace     bool     `json:"changeWorkplace,omitempty"`
	ChangeEndDate       bool     `json:"changeEndDate,omitempty"`
	ChangePay           bool     `json:"changePay,omitempty"`
	ModifyCredentials   bool     `json:"modifyCredentials,omitempty"`
	NewUsername         string   `json:"newUsername,omitempty"`
	NewEmail            string   `json:"newEmail,omitempty"`
	RequirementID       string   `json:"requirementId,omitempty"`
	RequirementIDs      []string `json:"requirementIds,omitempty"`
}

func reqDate(v string) bool { return v != "" && validDate(v) }

// contractCur: el contrato vigente contra el que se valida una operación.
type contractCur struct {
	Start, End, AreaID, Position, Schedule, SiteID, Location, Project string
}

func curOf(cd *contractDoc) contractCur {
	c := contractCur{Start: cd.StartDate, End: dateOnly(cd.EndDate), AreaID: cd.Area.ID, Position: cd.Position, Schedule: cd.Schedule,
		Location: cd.Location, Project: cd.ProjectService}
	if cd.Site != nil {
		c.SiteID = cd.Site.ID
	}
	return c
}

// validate revisa la operación contra el contrato vigente y deja solo lo que aplica. Un TRANSFER (asistente anterior)
// se convierte en movimiento con cambio de área y los demás cambios que difieran del contrato.
func (in *ContractOpInput) validate(cur contractCur) error {
	start, end, areaID := cur.Start, cur.End, cur.AreaID
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	if in.Type == OpTransfer {
		in.Type, in.ChangeArea, in.ChangePay = OpMovement, true, in.Pay != nil
		in.ChangePosition = strings.TrimSpace(in.Position) != "" && strings.TrimSpace(in.Position) != cur.Position
		in.ChangeSchedule = strings.TrimSpace(in.Schedule) != "" && strings.TrimSpace(in.Schedule) != cur.Schedule
		in.ChangeWorkplace = in.SiteID != cur.SiteID || strings.TrimSpace(in.Location) != cur.Location || strings.TrimSpace(in.ProjectService) != cur.Project
		in.ChangeEndDate = (in.Indefinite && cur.End != "") || (!in.Indefinite && in.EndDate != "" && in.EndDate != dateOnly(cur.End))
		if strings.TrimSpace(in.Reason) == "" {
			in.Reason = "Traspaso de área"
		}
	}
	in.Reason, in.Position, in.Schedule = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Position), strings.TrimSpace(in.Schedule)
	in.Location, in.ProjectService = strings.TrimSpace(in.Location), strings.TrimSpace(in.ProjectService)
	in.NewUsername, in.NewEmail = strings.TrimSpace(in.NewUsername), strings.ToLower(strings.TrimSpace(in.NewEmail))
	start, end = dateOnly(start), dateOnly(end)
	if !maxLen(500, in.Reason) || !maxLen(150, in.Position) || !maxLen(200, in.Schedule, in.Location, in.ProjectService) ||
		!validDate(in.Date) || !validDate(in.EndDate) || !validDate(in.EquipmentReturnDate) {
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
	case OpMovement:
		if !(in.ChangeArea || in.ChangePosition || in.ChangeSchedule || in.ChangeWorkplace || in.ChangeEndDate || in.ChangePay) {
			return domain.NewError("MOVEMENT_NO_CHANGE")
		}
		if in.Reason == "" {
			return domain.NewError("REASON_REQUIRED")
		}
		if err := afterStart(in.Date); err != nil {
			return err
		}
		out := ContractOpInput{Type: OpMovement, Date: in.Date, Reason: in.Reason}
		if in.ChangeArea {
			if !validUUID(in.AreaID) {
				return domain.ErrValidation
			}
			if in.AreaID == areaID {
				return domain.NewError("TRANSFER_SAME_AREA")
			}
			out.ChangeArea, out.AreaID = true, in.AreaID
		}
		if in.ChangePosition {
			if in.Position == "" {
				return domain.ErrValidation
			}
			if in.Position == cur.Position {
				return domain.NewError("MOVEMENT_NO_CHANGE")
			}
			out.ChangePosition, out.Position = true, in.Position
		}
		if in.ChangeSchedule {
			if in.Schedule == "" {
				return domain.ErrValidation
			}
			if in.Schedule == cur.Schedule {
				return domain.NewError("MOVEMENT_NO_CHANGE")
			}
			out.ChangeSchedule, out.Schedule = true, in.Schedule
		}
		if in.ChangeWorkplace {
			if !optUUID(in.SiteID) {
				return domain.ErrValidation
			}
			if in.SiteID == cur.SiteID && in.Location == cur.Location && in.ProjectService == cur.Project {
				return domain.NewError("MOVEMENT_NO_CHANGE")
			}
			out.ChangeWorkplace, out.SiteID, out.Location, out.ProjectService = true, in.SiteID, in.Location, in.ProjectService
		}
		if in.ChangeEndDate {
			if in.Indefinite {
				if end == "" {
					return domain.NewError("MOVEMENT_NO_CHANGE")
				}
				out.Indefinite = true
			} else {
				if !reqDate(in.EndDate) || in.EndDate <= in.Date || in.EndDate < start {
					return domain.NewError("CONTRACT_DATE_INVALID")
				}
				if in.EndDate == end {
					return domain.NewError("MOVEMENT_NO_CHANGE")
				}
				out.EndDate = in.EndDate
			}
			out.ChangeEndDate = true
		}
		if in.ChangePay {
			if in.Pay == nil || !optUUID(in.CurrencyID) {
				return domain.ErrValidation
			}
			for _, v := range []float64{in.Pay.Base, in.Pay.Mobility, in.Pay.Quarterly, in.Pay.Semiannual, in.Pay.Fee} {
				if !validAmount(v) {
					return domain.ErrValidation
				}
			}
			if !validPay(in.Pay) {
				return domain.NewError("PAY_REQUIRED")
			}
			out.ChangePay, out.Pay, out.CurrencyID = true, in.Pay, in.CurrencyID
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
			out.ModifyCredentials, out.NewUsername, out.NewEmail = true, in.NewUsername, in.NewEmail
		}
		*in = out
	case OpRQChange:
		ids := append([]string{}, in.RequirementIDs...)
		if in.RequirementID != "" {
			ids = append([]string{in.RequirementID}, ids...)
		}
		uniq, seen := []string{}, map[string]bool{}
		for _, id := range ids {
			id = strings.ToLower(strings.TrimSpace(id))
			if !validUUID(id) {
				return domain.ErrValidation
			}
			if !seen[id] {
				seen[id] = true
				uniq = append(uniq, id)
			}
		}
		if len(uniq) == 0 || len(uniq) > 10 {
			return domain.ErrValidation
		}
		if err := afterStart(in.Date); err != nil {
			return err
		}
		*in = ContractOpInput{Type: in.Type, Date: in.Date, Reason: in.Reason, RequirementIDs: uniq}
	default:
		return domain.ErrValidation
	}
	return nil
}

type idLabel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
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
	Client         idLabel  `json:"client"`
	Requirement    string   `json:"requirement"`
	RequirementID  string   `json:"requirementId"`
	Position       string   `json:"position"`
	Area           idLabel  `json:"area"`
	Site           *idLabel `json:"site"`
	Location       string   `json:"location"`
	ProjectService string   `json:"projectService"`
	Currency       struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	} `json:"currency"`
	PayEnc     string       `json:"payEnc"`
	Schedule   string       `json:"schedule"`
	StartDate  string       `json:"startDate"`
	EndDate    string       `json:"endDate"`
	Users      *UserRequest `json:"users"`
	Credential *struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Status   string `json:"status"`
	} `json:"credential"`
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
	ClientID   string `json:"clientId,omitempty"`
	Client     string `json:"client"`
	AreaID     string `json:"areaId,omitempty"`
	Area       string `json:"area"`
	Position   string `json:"position"`
	Schedule   string `json:"schedule"`
	SiteID     string `json:"siteId,omitempty"`
	Site       string `json:"site,omitempty"`
	Location   string `json:"location,omitempty"`
	Project    string `json:"project,omitempty"`
	CurrencyID string `json:"currencyId,omitempty"`
	Currency   string `json:"currency,omitempty"`
	StartDate  string `json:"startDate"`
	EndDate    string `json:"endDate,omitempty"`
	RQ         string `json:"rq,omitempty"`
}

// rqLabel: RQ destino de un cambio de RQ.
type rqLabel struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Client string `json:"client,omitempty"`
}

type opLabels struct {
	Client            string    `json:"client,omitempty"`
	Area              string    `json:"area,omitempty"`
	Site              string    `json:"site,omitempty"`
	Currency          string    `json:"currency,omitempty"`
	TerminationReason string    `json:"terminationReason,omitempty"`
	RQCode            string    `json:"rqCode,omitempty"`
	RQTitle           string    `json:"rqTitle,omitempty"`
	RQClient          string    `json:"rqClient,omitempty"`
	RQs               []rqLabel `json:"rqs,omitempty"`
}

// equipItem: equipo a recuperar (de la ficha de equipo aprobada).
type equipItem struct {
	Label       string `json:"label"`
	Detail      string `json:"detail,omitempty"`
	DeliveredAt string `json:"deliveredAt,omitempty"`
}

// opChange: cambio de fecha o anulación de una baja programada (solo para los correos de aviso).
type opChange struct {
	From, To, Reason string
}

// contractOpData: lo que se guarda de la operación (hire_contract_op.data); con esto se reimprimen sus documentos. La
// remuneración nueva y la anterior van cifradas (payEnc, beforePayEnc); la API las quita antes de responder.
type contractOpData struct {
	Type         string           `json:"type"`
	Input        ContractOpInput  `json:"input"`
	Before       contractSnapshot `json:"before"`
	Labels       opLabels         `json:"labels"`
	Equipment    []equipItem      `json:"equipment,omitempty"`
	Users        *UserRequest     `json:"users,omitempty"`
	Scheduled    bool             `json:"scheduled,omitempty"`
	Changed      []string         `json:"changed,omitempty"` // movimiento: AREA, POSITION, SCHEDULE, WORKPLACE, END_DATE, PAY
	Changes      []FieldChange    `json:"changes,omitempty"` // para el historial cuando se aplica (baja o movimiento programados)
	PayEnc       string           `json:"payEnc,omitempty"`
	BeforePayEnc string           `json:"beforePayEnc,omitempty"`
	newPay       *Pay
	beforePay    *Pay
	change       *opChange
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

func limaDate() string { return time.Now().In(limaTZ()).Format("2006-01-02") }

// buildOpData valida contra los catálogos y BDT y arma lo que se guarda de la operación.
func (s *SelectionService) buildOpData(ctx context.Context, p *domain.Principal, tid uint64, cd *contractDoc, in ContractOpInput) (*contractOpData, error) {
	op := &contractOpData{Type: in.Type, Input: in, Users: cd.Users, BeforePayEnc: cd.PayEnc,
		Before: contractSnapshot{ClientID: cd.Client.ID, Client: cd.Client.Label, AreaID: cd.Area.ID, Area: cd.Area.Label, Position: cd.Position,
			Schedule: cd.Schedule, Location: cd.Location, Project: cd.ProjectService, CurrencyID: cd.Currency.ID, Currency: cd.Currency.Code,
			StartDate: dateOnly(cd.StartDate), EndDate: dateOnly(cd.EndDate), RQ: cd.Requirement}}
	if cd.Site != nil {
		op.Before.SiteID, op.Before.Site = cd.Site.ID, cd.Site.Label
	}
	// credenciales vigentes del colaborador (las de su registro; si no, las de la ficha de usuarios del ingreso)
	if c := cd.Credential; c != nil && c.Status == "ACTIVE" {
		op.Users = &UserRequest{Username: c.Username, Email: c.Email}
	}
	ids := nonEmpty([]string{in.AreaID, in.SiteID, in.CurrencyID, in.TerminationReasonID})
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
		op.Labels.Area, op.Labels.Site, op.Labels.TerminationReason = labels[in.AreaID].Label, labels[in.SiteID].Label, labels[in.TerminationReasonID].Label
		op.Labels.Currency = labels[in.CurrencyID].Code
	}
	switch in.Type {
	case OpTerminate:
		op.Scheduled = in.Date > limaDate()
		if in.RecoverEquipment {
			for _, e := range cd.Equipment {
				if e.Status == "DELIVERED" || e.Status == "PENDING_RETURN" {
					op.Equipment = append(op.Equipment, equipItem{Label: e.Label, Detail: equipDetail(e.Details), DeliveredAt: dateOnly(e.DeliveredAt)})
				}
			}
		}
	case OpMovement:
		op.Scheduled = in.Date > limaDate()
		if op.Labels.Currency == "" {
			op.Labels.Currency = cd.Currency.Code
		}
		for _, x := range []struct {
			on   bool
			code string
		}{{in.ChangeArea, "AREA"}, {in.ChangePosition, "POSITION"}, {in.ChangeSchedule, "SCHEDULE"}, {in.ChangeWorkplace, "WORKPLACE"},
			{in.ChangeEndDate, "END_DATE"}, {in.ChangePay, "PAY"}} {
			if x.on {
				op.Changed = append(op.Changed, x.code)
			}
		}
		if in.ChangePay && in.Pay != nil {
			enc, err := s.encryptPay(in.Pay)
			if err != nil {
				return nil, err
			}
			op.PayEnc, op.newPay = base64.StdEncoding.EncodeToString(enc), in.Pay
		}
		op.Input.Pay = nil // nunca en claro en la base
	case OpRQChange:
		for _, id := range in.RequirementIDs {
			if id == cd.RequirementID {
				return nil, domain.ErrValidation
			}
			raw, err := s.Repo.Requirement(ctx, meta(p, ""), tid, id)
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
			op.Labels.RQs = append(op.Labels.RQs, rqLabel{ID: id, Code: rq.Code, Title: rq.Title, Client: rq.Client.Label})
		}
		first := op.Labels.RQs[0]
		op.Labels.RQCode, op.Labels.RQTitle, op.Labels.RQClient = first.Code, first.Title, first.Client
	}
	return op, nil
}

// rqCodes: «RQ-1, RQ-2» de un cambio de RQ.
func (op *contractOpData) rqCodes() string {
	if len(op.Labels.RQs) == 0 {
		return op.Labels.RQCode
	}
	out := []string{}
	for _, r := range op.Labels.RQs {
		out = append(out, r.Code)
	}
	return strings.Join(out, ", ")
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
	case OpMovement:
		if in.ChangeArea {
			add("area", b.Area, op.Labels.Area)
		}
		if in.ChangePosition {
			add("position", b.Position, in.Position)
		}
		if in.ChangeSchedule {
			add("schedule", b.Schedule, in.Schedule)
		}
		if in.ChangeWorkplace {
			add("site", b.Site, op.Labels.Site)
			add("location", b.Location, in.Location)
			add("projectService", b.Project, in.ProjectService)
		}
		if in.ChangeEndDate {
			add("endDate", b.EndDate, in.EndDate)
		}
		if in.ChangePay {
			add("currency", b.Currency, op.Labels.Currency)
			out = append(out, FieldChange{Field: "pay"})
		}
		add("movementDate", "", in.Date)
		if in.ModifyCredentials {
			add("userRequest", "", strings.Join(nonEmpty([]string{in.NewUsername, in.NewEmail}), " · "))
		}
	case OpRQChange:
		add("requirement", b.RQ, op.rqCodes())
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
	case OpMovement, OpTransfer:
		if op.Input.ModifyCredentials {
			return []string{"FMI", "USERS"}
		}
	case OpRQChange:
		return []string{"FMI", "NOTICE"}
	}
	return []string{"FMI"}
}

// ContractOpResult: operación registrada, su estado (EXECUTED | SCHEDULED | REQUESTED) y los correos que se envían.
type ContractOpResult struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Status    string   `json:"status"`
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
	if err := in.validate(curOf(cd)); err != nil {
		return nil, nil, err
	}
	if in.Pay != nil && !p.Has("employee.salary.read") { // sin acceso a remuneraciones no se cambia la remuneración
		return nil, nil, domain.ErrForbidden
	}
	op, err := s.buildOpData(ctx, p, tid, cd, in)
	if err != nil {
		return nil, nil, err
	}
	return cd, op, nil
}

// ContractOp ejecuta la operación de fin de contrato y envía sus correos (firmados por quien la confirma).
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
	today := limaDate()
	date := in.Date
	if in.Type == OpRenew {
		date = today
	}
	var payEnc []byte
	if op.PayEnc != "" {
		payEnc, _ = base64.StdEncoding.DecodeString(op.PayEnc)
	}
	op.Changes = op.changes()
	data, _ := json.Marshal(op)
	changes, _ := json.Marshal(op.Changes)
	id, status, err := s.Repo.HireContractOp(ctx, meta(p, ip), tid, ports.ContractOpWrite{RequestID: requestID, OpID: uuid.NewString(), Type: in.Type,
		Date: date, EndDate: in.EndDate, Today: today, TerminationReasonID: in.TerminationReasonID, Recover: in.RecoverEquipment,
		ReturnDate: in.EquipmentReturnDate, AreaID: in.AreaID, Position: in.Position, Schedule: in.Schedule, SiteID: in.SiteID,
		Location: in.Location, Project: in.ProjectService, CurrencyID: in.CurrencyID, PayEnc: payEnc, RQIDs: in.RequirementIDs,
		Reason: in.Reason, Data: data, Changes: changes})
	if err != nil {
		return nil, err
	}
	s.contractDispatchAsync(p, tid, requestID, id, op, opReason[op.Type], signerOf(p))
	return &ContractOpResult{ID: id, Type: op.Type, Status: status, Documents: opForms(op)}, nil
}

// ChangeScheduledOp cambia la fecha (RESCHEDULE) o anula (CANCEL) una baja programada, con motivo, y avisa a los mismos
// destinatarios. Si la nueva fecha ya llegó, la baja se hace efectiva al momento. Devuelve el estado final.
func (s *SelectionService) ChangeScheduledOp(ctx context.Context, p *domain.Principal, ip, requestID, opID, action, date, reason string) (string, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return "", err
	}
	action, reason = strings.ToUpper(strings.TrimSpace(action)), strings.TrimSpace(reason)
	if !validUUID(requestID) || !validUUID(opID) || (action != "RESCHEDULE" && action != "CANCEL") || !maxLen(500, reason) ||
		(action == "RESCHEDULE" && !reqDate(date)) {
		return "", domain.ErrValidation
	}
	if reason == "" {
		return "", domain.NewError("REASON_REQUIRED")
	}
	raw, err := s.Repo.HireContractOpGet(ctx, meta(p, ""), tid, requestID, opID)
	if err != nil {
		return "", err
	}
	var x struct {
		Data contractOpData `json:"data"`
	}
	if err := json.Unmarshal(raw, &x); err != nil || (x.Data.Type != OpTerminate && x.Data.Type != OpMovement) {
		return "", domain.NewError("CONTRACT_NOT_SCHEDULED")
	}
	if action == "CANCEL" {
		date = ""
	}
	status, err := s.Repo.HireContractOpChange(ctx, meta(p, ip), tid, requestID, opID, action, date, reason, limaDate())
	if err != nil {
		return "", err
	}
	op := &x.Data
	op.change = &opChange{From: op.Input.Date, To: date, Reason: reason}
	reasonCode := "OP_CANCELLED"
	if action == "RESCHEDULE" {
		op.Input.Date, op.Scheduled, reasonCode = date, date > limaDate(), "RESCHEDULED"
	}
	s.contractDispatchAsync(p, tid, requestID, opID, op, reasonCode, signerOf(p))
	return status, nil
}

// ContractPreview: PDF (FMI o USERS) de la operación antes de confirmarla (firma el usuario que la confirmará).
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
	c, err := s.contractContext(ctx, p, tid, requestID, op, false, signerOf(p))
	if err != nil {
		return nil, "", err
	}
	b, err := s.Renderer.Render(contractPrint(form, c, op))
	if err != nil {
		return nil, "", err
	}
	return b, fileName(form, c.talentName()), nil
}

// ResendContractOp reenvía los documentos de una operación ya registrada; firma quien la confirmó (o cambió su fecha).
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
		Data     contractOpData `json:"data"`
		Status   string         `json:"status"`
		SignerID string         `json:"signerId"`
		Signer   string         `json:"signerName"`
		Email    string         `json:"signerEmail"`
	}
	if err := json.Unmarshal(decryptFields(s.Cipher, decryptNames(s.Cipher, raw), "signerEmailEnc"), &x); err != nil || x.Data.Type == "" {
		return domain.ErrNotFound
	}
	x.Data.Scheduled = x.Status == "SCHEDULED"
	s.contractDispatchAsync(p, tid, requestID, opID, &x.Data, "RESENT", signer{ID: x.SignerID, Name: x.Signer, Email: x.Email})
	return nil
}

// ApplyScheduledTerminations hace efectivas las bajas programadas cuya fecha llegó (rutina diaria; idempotente).
func (s *SelectionService) ApplyScheduledTerminations(ctx context.Context, today time.Time) (int, error) {
	return s.Repo.ContractApplyScheduled(ctx, today)
}

// RunScheduledJob: ejecución manual de la rutina de bajas programadas (Super Admin). La fecha solo se puede simular
// fuera de producción (pruebas controladas en staging).
func (s *SelectionService) RunScheduledJob(ctx context.Context, date string) (int, error) {
	today := time.Now().In(limaTZ())
	if date != "" {
		if s.Env == "production" || s.Env == "preprod" {
			return 0, domain.ErrForbidden
		}
		d, err := time.ParseInLocation("2006-01-02", date, limaTZ())
		if err != nil {
			return 0, domain.ErrValidation
		}
		today = d
	}
	return s.ApplyScheduledTerminations(ctx, today)
}

// Contract: contexto del asistente: contrato, nivel de aviso, operaciones, equipo, credenciales y a quién va cada correo.
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
	delete(m, "payEnc")
	if p.Has("employee.salary.read") {
		if pay := s.decryptPay(cd.PayEnc); pay != nil {
			m["pay"] = pay
		}
	}
	m["canEditPay"] = p.Has("employee.salary.read")
	st, err := s.settings(ctx, tid)
	if err != nil {
		return nil, err
	}
	m["alertDays"] = st.ContractAlertDays
	m["mailer"] = s.Mail != nil && mailerEnabled(s.Mail)
	m["today"] = limaDate()
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

// signer: quien firma los documentos de la operación (quien la confirmó). ID uuid del usuario; si UserID > 0 se usa ese.
type signer struct {
	UserID uint64
	ID     string
	Name   string
	Email  string
}

func signerOf(p *domain.Principal) signer {
	return signer{UserID: p.UserID, Name: p.FullName, Email: p.Email}
}

// contractContext: contexto de impresión del contrato con la foto anterior a la operación, firmado por quien la confirmó.
func (s *SelectionService) contractContext(ctx context.Context, p *domain.Principal, tid uint64, requestID string, op *contractOpData, internal bool, sg signer) (*formContext, error) {
	c, err := s.formContextFor(ctx, p, tid, requestID, "FMI", internal)
	if err != nil {
		return nil, err
	}
	if sg.Name != "" {
		c.manager = sg.Name
	}
	c.signature = nil
	if sig, err := s.signature(ctx, tid, sg.UserID, sg.ID); err == nil {
		c.signature = sig
	}
	if c.withPay {
		op.beforePay = s.decryptPay(op.BeforePayEnc)
		if op.newPay == nil && op.PayEnc != "" {
			op.newPay = s.decryptPay(op.PayEnc)
		}
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
		h.Location, h.ProjectService = b.Location, b.Project
	}
	if c.labels == nil {
		c.labels = map[string]ports.CatalogLabel{}
	}
	if b.AreaID != "" {
		h.AreaID = b.AreaID
		c.labels[b.AreaID] = ports.CatalogLabel{Label: b.Area}
	}
	if b.SiteID != "" {
		h.SiteID = b.SiteID
		c.labels[b.SiteID] = ports.CatalogLabel{Label: b.Site}
	}
	if b.CurrencyID != "" {
		h.CurrencyID = b.CurrencyID
		c.labels[b.CurrencyID] = ports.CatalogLabel{Code: b.Currency, Label: b.Currency}
	}
	if op.beforePay != nil {
		h.Pay = op.beforePay
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

// payParts: montos de una remuneración para la estructura salarial de la FMI.
func payParts(p *Pay, sym string, rxh bool) (base, mob, bonus string, hasBase, hasMob, quarterly bool) {
	if p == nil {
		return
	}
	if rxh || (p.Fee > 0 && p.Base == 0) {
		return amount(sym, p.Fee), "", "", p.Fee > 0, false, false
	}
	b := []string{}
	if p.Quarterly > 0 {
		b = append(b, amount(sym, p.Quarterly))
	}
	if p.Semiannual > 0 {
		b = append(b, amount(sym, p.Semiannual)+" (semestral)")
	}
	return amount(sym, p.Base), amount(sym, p.Mobility), strings.Join(b, " · "), p.Base > 0, p.Mobility > 0, p.Quarterly > 0
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
			if op.Scheduled {
				note(f, fmt.Sprintf("Baja programada: se hace efectiva el %s.", fmtDate(in.Date)))
			}
			if in.Reason != "" {
				note(f, "Baja: "+in.Reason)
			}
		case OpMovement:
			b := op.Before
			arrow := func(from, to string) string { return orDash(from) + " → " + orDash(to) }
			if in.ChangePosition {
				f.MovePosition = arrow(b.Position, in.Position)
			}
			if in.ChangeArea {
				f.MoveArea = arrow(b.Area, op.Labels.Area)
			}
			if in.ChangeSchedule {
				f.MoveSchedule = arrow(b.Schedule, in.Schedule)
			}
			if in.ChangeWorkplace {
				if op.Labels.Site != b.Site {
					f.MoveSite = arrow(b.Site, op.Labels.Site)
				}
				if in.ProjectService != b.Project {
					f.MoveProject = arrow(b.Project, in.ProjectService)
				}
				if in.Location != b.Location {
					note(f, "Lugar de trabajo: "+arrow(b.Location, in.Location)+".")
				}
			}
			if in.ChangeEndDate {
				f.MoveEnd = arrow(endText(b.EndDate), endText(in.EndDate))
			}
			f.MoveDate = fmtDate(in.Date)
			if in.ChangePay {
				if !c.withPay {
					f.MoveBase = "Reservado"
				} else if op.newPay != nil {
					rxh := c.labels[c.forms.Hire.ContractID].Str1 == "RXH"
					f.MoveBase, f.MoveMobility, f.MoveBonus, f.MoveHasBase, f.MoveHasMobility, f.MoveQuarterly =
						payParts(op.newPay, currencySymbol(op.Labels.Currency), rxh)
				}
			}
			if op.Scheduled {
				note(f, fmt.Sprintf("Movimiento programado: se aplica el %s.", fmtDate(in.Date)))
			}
			if in.Reason != "" {
				note(f, "Motivo del movimiento: "+in.Reason)
			}
		case OpRQChange:
			f.TermReason = "Cambio a otro requerimiento: " + op.rqCodes()
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

func (s *SelectionService) contractDispatchAsync(p *domain.Principal, tid uint64, requestID, opID string, op *contractOpData, reason string, sg signer) {
	if s.Renderer == nil || s.Mail == nil {
		return
	}
	pc := *p
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := s.contractDispatch(ctx, &pc, tid, requestID, opID, op, reason, sg); err != nil {
			slog.Warn("no se pudieron enviar los documentos del fin de contrato", "request", requestID, "op", opID, "err", err)
		}
	}()
}

// contractDispatch envía cada documento de la operación y registra el resultado (enlazado a la operación). En una
// anulación no se adjuntan los PDF.
func (s *SelectionService) contractDispatch(ctx context.Context, p *domain.Principal, tid uint64, requestID, opID string, op *contractOpData, reason string, sg signer) error {
	log := func(form string, rcpt []string, status, errText string) {
		_ = s.Repo.HireOpDispatchAdd(ctx, p.UserID, tid, requestID, opID, form, reason, rcpt, status, errText)
	}
	c, err := s.contractContext(ctx, p, tid, requestID, op, true, sg)
	if err != nil {
		for _, f := range opForms(op) {
			log(f, nil, "FAILED", err.Error())
		}
		return err
	}
	approver := sg.Email
	if approver == "" {
		approver = p.Email
	}
	for _, form := range opForms(op) {
		to, cc, greet := contractRecipients(form, c, approver)
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
		if (form == "FMI" || form == "USERS") && reason != "OP_CANCELLED" {
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
