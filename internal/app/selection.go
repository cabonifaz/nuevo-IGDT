package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// SelectionService: bandeja de selección. BDT publica (API de integración) sus RQ con el avance de los postulantes y
// los candidatos confirmados, que aquí se vuelven solicitudes de ingreso para los gestores de servicios.
type SelectionService struct {
	Repo     ports.SelectionRepository
	Cipher   ports.Cipher
	Notifier *NotificationService
	Features *FeatureService
	Renderer ports.FormRenderer // fichas en PDF
	Mail     ports.MailSender   // envío de las fichas a sus destinatarios
	BDT      ports.BDTReader    // perfil, CV y postulaciones del candidato en BDT
	Storage  ports.FileStorage  // logo de las fichas
	Files    ports.FileRepository
	AppURL   string
}

// SelectionRoles: roles que reciben los avisos de solicitudes de ingreso.
var SelectionRoles = []string{"SERVICE_MANAGER", "TENANT_ADMIN"}

// ---------------- Contrato de BDT ----------------

type bdtRef struct {
	ID    string `json:"id,omitempty"`
	Code  string `json:"code,omitempty"`
	Label string `json:"label"`
}

type bdtStage struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Order int    `json:"order"`
}

type bdtVacancy struct {
	ID          string   `json:"id"`
	Profile     bdtRef   `json:"profile"`
	Quantity    int      `json:"quantity"`
	Covered     int      `json:"covered"`
	RateInitial *float64 `json:"rateInitial,omitempty"`
	RateFinal   *float64 `json:"rateFinal,omitempty"`
}

type bdtCandidate struct {
	ID        string   `json:"id"`
	VacancyID string   `json:"vacancyId"`
	TalentID  string   `json:"talentId"`
	Name      string   `json:"name"`
	Headline  string   `json:"headline,omitempty"`
	Stage     bdtStage `json:"stage"`
	StageAt   string   `json:"stageAt,omitempty"`
	AddedAt   string   `json:"addedAt,omitempty"`
	Resigned  bool     `json:"resigned,omitempty"`
	Hired     bool     `json:"hired,omitempty"`
	StartDate string   `json:"startDate,omitempty"`
}

type bdtConditions struct {
	WorkModality string `json:"workModality,omitempty"`
	Duration     string `json:"duration,omitempty"`
	Contract     string `json:"contract,omitempty"`
	Emo          bool   `json:"emo"`
	Sctr         bool   `json:"sctr"`
	Laptop       bool   `json:"laptop"`
}

// BDTRequirement: snapshot de un RQ tal como lo envía BDT.
type BDTRequirement struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	StatusIn    struct {
		Code  string `json:"code"`
		Label string `json:"label"`
		Group string `json:"group"`
	} `json:"status"`
	Client      bdtRef         `json:"client"`
	Owner       string         `json:"owner,omitempty"`
	RequestDate string         `json:"requestDate,omitempty"`
	DueDate     string         `json:"dueDate,omitempty"`
	ClosedAt    string         `json:"closedAt,omitempty"`
	Conditions  bdtConditions  `json:"conditions"`
	Stages      []bdtStage     `json:"stages"`
	Vacancies   []bdtVacancy   `json:"vacancies"`
	Candidates  []bdtCandidate `json:"candidates"`
	StageCounts map[string]int `json:"stageCounts"`
	CreatedAt   string         `json:"createdAt,omitempty"`
	Alerts      *bdtAlerts     `json:"alerts,omitempty"`
	// calculados al recibir (tablero): última entrada a la etapa de cada postulante en proceso (ni confirmado ni renunciado)
	InProcessAt []string  `json:"inProcessAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Deleted     bool      `json:"deleted"`
}

// bdtAlerts: días hábiles sin movimiento para la alerta de atención y la crítica (configuración de BDT).
type bdtAlerts struct {
	Enabled      bool `json:"enabled"`
	WarnDays     int  `json:"warnDays"`
	CriticalDays int  `json:"criticalDays"`
}

// BDTHire: estado de un candidato confirmado (Active = sigue confirmado en BDT).
type BDTHire struct {
	ID          string `json:"id"`
	Active      bool   `json:"active"`
	Requirement struct {
		ID    string `json:"id"`
		Code  string `json:"code"`
		Title string `json:"title"`
	} `json:"requirement"`
	Client  bdtRef `json:"client"`
	Vacancy struct {
		ID          string   `json:"id"`
		Profile     bdtRef   `json:"profile"`
		RateInitial *float64 `json:"rateInitial,omitempty"`
		RateFinal   *float64 `json:"rateFinal,omitempty"`
	} `json:"vacancy"`
	Talent struct {
		ID             string `json:"id"`
		FirstName      string `json:"firstName"`
		LastName       string `json:"lastName"`
		SecondLastName string `json:"secondLastName"`
		Headline       string `json:"headline"`
		DocType        bdtRef `json:"docType"`
		DocNumber      string `json:"docNumber"`
		Email          string `json:"email"`
		Phone          string `json:"phone"`
	} `json:"talent"`
	Conditions  bdtConditions `json:"conditions"`
	ConfirmedBy string        `json:"confirmedBy"`
	ConfirmedAt time.Time     `json:"confirmedAt"`
}

// SyncResult: resultado por elemento de un envío de BDT (Error = código si se rechazó).
type SyncResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Applied bool   `json:"applied,omitempty"`
	Event   string `json:"event,omitempty"`
	Error   string `json:"error,omitempty"`
}

const maxSyncBatch = 50

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func optUUID(s string) bool { return s == "" || validUUID(s) }

func optDate(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ReceiveRequirements guarda los RQ que envía BDT (cada uno se valida por separado).
func (s *SelectionService) ReceiveRequirements(ctx context.Context, c *domain.ApiClient, items []json.RawMessage) ([]SyncResult, error) {
	if len(items) == 0 || len(items) > maxSyncBatch {
		return nil, domain.ErrValidation
	}
	out := make([]SyncResult, 0, len(items))
	for _, raw := range items {
		var q BDTRequirement
		if json.Unmarshal(raw, &q) != nil || !validUUID(q.ID) {
			out = append(out, SyncResult{ID: q.ID, Error: "VALIDATION"})
			continue
		}
		res := SyncResult{ID: q.ID}
		w, ok := normalizeRequirement(&q)
		if !ok {
			res.Error = "VALIDATION"
			out = append(out, res)
			continue
		}
		applied, err := s.Repo.RequirementUpsert(ctx, c.TenantID, w)
		if err != nil {
			res.Error = domain.AsError(err).Code
		} else {
			res.OK, res.Applied = true, applied
		}
		out = append(out, res)
	}
	return out, nil
}

func normalizeRequirement(q *BDTRequirement) (ports.RequirementWrite, bool) {
	st := q.StatusIn
	q.Code, q.Title = strings.TrimSpace(q.Code), strings.TrimSpace(q.Title)
	if q.Deleted && q.Code == "" {
		q.Code, q.Title = "-", "-"
	}
	if q.Code == "" || q.Title == "" || !maxLen(40, q.Code, st.Code) || !maxLen(200, q.Title, q.Client.Label, q.Owner) ||
		!maxLen(2000, q.Description) || !maxLen(100, st.Label) || (st.Group != "OPEN" && st.Group != "CLOSED") ||
		!optUUID(q.Client.ID) || !validDate(q.RequestDate) || !validDate(q.DueDate) || q.UpdatedAt.IsZero() ||
		len(q.Vacancies) > 200 || len(q.Candidates) > 2000 || len(q.Stages) > 30 {
		if !(q.Deleted && st.Group == "") {
			return ports.RequirementWrite{}, false
		}
		st.Group, st.Code, st.Label = "CLOSED", "DELETED", "Eliminado"
	}
	if a := q.Alerts; a != nil && (a.WarnDays < 1 || a.WarnDays > 90 || a.CriticalDays <= a.WarnDays || a.CriticalDays > 180) {
		q.Alerts = nil
	}
	if q.CreatedAt != "" && !maxLen(40, q.CreatedAt) {
		q.CreatedAt = ""
	}
	vacs, covered := 0, 0
	vacIdx := map[string]int{}
	for i, v := range q.Vacancies {
		if !validUUID(v.ID) || !optUUID(v.Profile.ID) || v.Quantity < 0 || !maxLen(200, v.Profile.Label) {
			return ports.RequirementWrite{}, false
		}
		vacs += v.Quantity
		q.Vacancies[i].Covered = 0
		vacIdx[v.ID] = i
	}
	q.StageCounts = map[string]int{}
	q.InProcessAt = []string{}
	for _, c := range q.Candidates {
		if !validUUID(c.ID) || !optUUID(c.VacancyID) || !optUUID(c.TalentID) || c.Stage.Code == "" ||
			!maxLen(250, c.Name, c.Headline) || !maxLen(40, c.Stage.Code, c.StageAt) {
			return ports.RequirementWrite{}, false
		}
		q.StageCounts[c.Stage.Code]++
		switch {
		case c.Resigned: // ya no cubre la vacante (igual que BDT)
		case c.Stage.Code == "CONFIRMED":
			covered++
			if i, ok := vacIdx[c.VacancyID]; ok {
				q.Vacancies[i].Covered++
			}
		default:
			q.InProcessAt = append(q.InProcessAt, c.StageAt)
		}
	}
	doc, err := json.Marshal(q)
	if err != nil {
		return ports.RequirementWrite{}, false
	}
	// el estado se guarda con su forma original ({code,label,group})
	var m map[string]any
	_ = json.Unmarshal(doc, &m)
	m["status"] = map[string]string{"code": st.Code, "label": st.Label, "group": st.Group}
	doc, _ = json.Marshal(m)
	return ports.RequirementWrite{ID: q.ID, Code: q.Code, Title: q.Title, ClientID: q.Client.ID, ClientLabel: q.Client.Label,
		StatusCode: st.Code, StatusLabel: st.Label, StatusGroup: st.Group, RequestDate: optDate(q.RequestDate), DueDate: optDate(q.DueDate),
		Vacancies: vacs, Covered: covered, Candidates: len(q.Candidates), Doc: doc, UpdatedAt: q.UpdatedAt, Deleted: q.Deleted}, true
}

// ReceiveHires registra los candidatos confirmados (o la cancelación de su confirmación) y avisa a los gestores.
func (s *SelectionService) ReceiveHires(ctx context.Context, c *domain.ApiClient, items []json.RawMessage) ([]SyncResult, error) {
	if len(items) == 0 || len(items) > maxSyncBatch {
		return nil, domain.ErrValidation
	}
	out := make([]SyncResult, 0, len(items))
	var events []hireEvent
	for _, raw := range items {
		var h BDTHire
		if json.Unmarshal(raw, &h) != nil || !validUUID(h.ID) {
			out = append(out, SyncResult{ID: h.ID, Error: "VALIDATION"})
			continue
		}
		res := SyncResult{ID: h.ID}
		w, err := s.hireWrite(&h)
		if err == nil {
			var rec *ports.HireReceived
			if rec, err = s.Repo.HireReceive(ctx, c.TenantID, c.KeyID, w); err == nil {
				res.OK, res.Event = true, rec.Event
				if rec.Event == "NEW" || rec.Event == "REOPENED" || rec.Event == "CANCELLED" {
					events = append(events, hireEvent{h: h, rec: *rec})
				}
			}
		}
		if err != nil {
			res.Error = domain.AsError(err).Code
		}
		out = append(out, res)
	}
	s.notifyHires(ctx, c, events)
	return out, nil
}

type hireEvent struct {
	h   BDTHire
	rec ports.HireReceived
}

// hireSummaryOver: con más avisos de un mismo tipo en un envío (p. ej. al conectar), se manda uno solo con el total.
const hireSummaryOver = 3

func (s *SelectionService) hireWrite(h *BDTHire) (ports.HireWrite, error) {
	t := &h.Talent
	t.FirstName, t.LastName = strings.TrimSpace(t.FirstName), strings.TrimSpace(t.LastName)
	w := ports.HireWrite{NewID: uuid.NewString(), CandidateID: h.ID, Active: h.Active, RequirementID: h.Requirement.ID, TalentID: t.ID}
	if !h.Active {
		// retiro de la confirmación: basta con el candidato (el resto se toma de la solicitud existente)
		if !optUUID(w.RequirementID) {
			return w, domain.ErrValidation
		}
		if w.RequirementID == "" {
			w.RequirementID = uuid.Nil.String()
		}
		w.TalentID, w.FirstName, w.LastName, w.Doc, w.ConfirmedAt = uuid.Nil.String(), "-", "-", json.RawMessage("{}"), time.Now()
		return w, nil
	}
	if !validUUID(h.Requirement.ID) || !validUUID(t.ID) || t.FirstName == "" || t.LastName == "" || h.ConfirmedAt.IsZero() ||
		!maxLen(100, t.FirstName, t.LastName, t.SecondLastName, t.DocType.Label) || !maxLen(200, t.Headline, h.Client.Label,
		h.Vacancy.Profile.Label, h.ConfirmedBy, h.Requirement.Title) || !maxLen(40, t.DocType.Code, h.Requirement.Code) ||
		!maxLen(30, t.DocNumber, t.Phone) || !optEmail(t.Email) || !optUUID(h.Client.ID) || !optUUID(h.Vacancy.Profile.ID) ||
		!optUUID(h.Vacancy.ID) {
		return w, domain.ErrValidation
	}
	var err error
	if w.DocNumberEnc, err = s.encrypt(strings.TrimSpace(t.DocNumber)); err != nil {
		return w, err
	}
	if w.EmailEnc, err = s.encrypt(strings.ToLower(strings.TrimSpace(t.Email))); err != nil {
		return w, err
	}
	if w.PhoneEnc, err = s.encrypt(strings.TrimSpace(t.Phone)); err != nil {
		return w, err
	}
	if d := strings.TrimSpace(t.DocNumber); d != "" {
		w.DocNumberBidx = s.Cipher.BlindIndex(d)
	}
	w.FirstName, w.LastName, w.SecondLastName, w.Headline = t.FirstName, t.LastName, strings.TrimSpace(t.SecondLastName), t.Headline
	w.DocTypeCode, w.DocTypeLabel = t.DocType.Code, t.DocType.Label
	w.ClientID, w.ClientLabel, w.ProfileID, w.ProfileLabel = h.Client.ID, h.Client.Label, h.Vacancy.Profile.ID, h.Vacancy.Profile.Label
	w.ConfirmedAt = h.ConfirmedAt
	// snapshot sin datos personales
	w.Doc, _ = json.Marshal(map[string]any{"requirement": map[string]string{"code": h.Requirement.Code, "title": h.Requirement.Title},
		"vacancy": h.Vacancy, "conditions": h.Conditions, "confirmedBy": h.ConfirmedBy})
	return w, nil
}

func (s *SelectionService) encrypt(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	return s.Cipher.Encrypt(v)
}

// notifyHires avisa a los gestores de las solicitudes nuevas o reabiertas y de las canceladas (agrupadas si son muchas).
func (s *SelectionService) notifyHires(ctx context.Context, c *domain.ApiClient, events []hireEvent) {
	if s.Notifier == nil || len(events) == 0 {
		return
	}
	if s.Features != nil {
		if on, err := s.Features.Enabled(ctx, c.TenantID); err == nil && !on["selection_inbox"] {
			return
		}
	}
	var news, cancelled []hireEvent
	for _, e := range events {
		if e.rec.Event == "CANCELLED" {
			cancelled = append(cancelled, e)
		} else {
			news = append(news, e)
		}
	}
	for _, group := range [][]hireEvent{news, cancelled} {
		if len(group) > hireSummaryOver {
			s.notifyHireSummary(ctx, c, group)
			continue
		}
		for i := range group {
			s.notifyHire(ctx, c, &group[i].h, &group[i].rec)
		}
	}
}

func (s *SelectionService) notifyHireSummary(ctx context.Context, c *domain.ApiClient, group []hireEvent) {
	cancel := group[0].rec.Event == "CANCELLED"
	typ, title := domain.NotifHireRequest, fmt.Sprintf("%d solicitudes de ingreso nuevas", len(group))
	if cancel {
		typ, title = domain.NotifHireCancelled, fmt.Sprintf("%d solicitudes de ingreso canceladas", len(group))
	}
	payload, _ := json.Marshal(map[string]any{"title": title, "count": len(group), "summary": true})
	s.send(ctx, c, typ, uuid.NewString(), payload, func(locale string) (string, string) {
		if strings.HasPrefix(locale, "en") {
			if cancel {
				return "Hire requests cancelled", fmt.Sprintf("%d requests", len(group))
			}
			return "New hire requests", fmt.Sprintf("%d requests from Recruiting", len(group))
		}
		return title, "Desde Selección (BDT)"
	}, "/"+c.TenantSlug+"/solicitudes")
}

func (s *SelectionService) notifyHire(ctx context.Context, c *domain.ApiClient, h *BDTHire, rec *ports.HireReceived) {
	typ := domain.NotifHireRequest
	if rec.Event == "CANCELLED" {
		typ = domain.NotifHireCancelled
	}
	name := strings.TrimSpace(h.Talent.FirstName + " " + h.Talent.LastName)
	payload, _ := json.Marshal(map[string]any{"title": name, "requirement": h.Requirement.Code, "client": h.Client.Label,
		"profile": h.Vacancy.Profile.Label, "event": rec.Event})
	body := name
	if h.Requirement.Code != "" {
		body += " · " + h.Requirement.Code
	}
	s.send(ctx, c, typ, rec.ID, payload, func(locale string) (string, string) {
		en := strings.HasPrefix(locale, "en")
		switch {
		case rec.Event == "CANCELLED" && en:
			return "Hire request cancelled", body
		case rec.Event == "CANCELLED":
			return "Solicitud de ingreso cancelada", body
		case en:
			return "New hire request", body
		}
		return "Nueva solicitud de ingreso", body
	}, "/"+c.TenantSlug+"/solicitudes/"+rec.ID)
}

// send registra la notificación para los gestores del tenant y envía el push en segundo plano.
func (s *SelectionService) send(ctx context.Context, c *domain.ApiClient, typ, entityID string, payload json.RawMessage,
	text func(locale string) (string, string), url string) {
	in := ports.NotifyInput{ID: uuid.NewString(), Type: typ, EntityType: "HIRE_REQUEST", EntityID: entityID, Payload: payload}
	res, err := s.Notifier.Repo.NotifyUsers(context.WithoutCancel(ctx), ports.Meta{}, c.TenantID, in, ports.AudienceTenantRoles, 0, SelectionRoles)
	if err != nil {
		slog.Warn("no se pudo registrar la notificación", "type", typ, "err", err)
		return
	}
	n := s.Notifier
	if len(res.Targets) == 0 || n.Push == nil || !n.Push.Enabled() {
		return
	}
	go n.deliver(res.ID, res.Targets, func(locale string) pushMessage {
		t, b := text(locale)
		return pushMessage{Title: t, Body: b, URL: url, Tag: "hire-" + entityID, ID: res.ID}
	})
}

// ---------------- Consultas de los gestores ----------------

func (s *SelectionService) Requirements(ctx context.Context, p *domain.Principal, scope string) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if scope == "" {
		scope = "OPEN"
	}
	if scope != "OPEN" && scope != "CLOSED" && scope != "ALL" {
		return nil, domain.ErrValidation
	}
	return s.Repo.Requirements(ctx, meta(p, ""), tid, scope)
}

func (s *SelectionService) Requirement(ctx context.Context, p *domain.Principal, id string) (json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(id) {
		return nil, domain.ErrValidation
	}
	return s.Repo.Requirement(ctx, meta(p, ""), tid, id)
}

func (s *SelectionService) Hires(ctx context.Context, p *domain.Principal, status string) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	var st *string
	if status != "" {
		if !contains([]string{"PENDING", "IN_PROGRESS", "COMPLETED", "CANCELLED", "REVOKED"}, status) {
			return nil, domain.ErrValidation
		}
		st = &status
	}
	list, err := s.Repo.Hires(ctx, meta(p, ""), tid, st)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i] = decryptNames(s.Cipher, list[i])
	}
	return list, nil
}

// Hire: detalle con los datos personales descifrados (la consulta queda auditada).
func (s *SelectionService) Hire(ctx context.Context, p *domain.Principal, ip, id string) (json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	if !validUUID(id) {
		return nil, domain.ErrValidation
	}
	doc, err := s.Repo.Hire(ctx, meta(p, ip), tid, id)
	if err != nil {
		return nil, err
	}
	out := decryptFields(s.Cipher, decryptNames(s.Cipher, doc), "docNumberEnc", "emailEnc", "phoneEnc")
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		return out, nil
	}
	st, err := s.settings(ctx, tid)
	if err != nil {
		return nil, err
	}
	m["formSettings"] = map[string]any{"corporateDomain": st.CorporateDomain, "editGraceDays": st.EditGraceDays,
		"recipients": map[string]int{"FMI": len(st.Recipients["FMI"]), "EQUIPMENT": len(st.Recipients["EQUIPMENT"]), "USERS": len(st.Recipients["USERS"])},
		"mailer":     s.Mail != nil && mailerEnabled(s.Mail)}
	switch m["status"] {
	case "COMPLETED", "REVOKED":
		hire, err := s.hireOf(ctx, p, tid, id)
		if err != nil {
			return nil, err
		}
		if hire != nil {
			delete(hire, "payEnc")
			if f, err := s.formsOfHire(ctx, p, tid, id); err == nil && f != nil {
				if !p.Has("employee.salary.read") {
					f.Hire.Pay = nil
				}
				hire["forms"] = f
			}
		}
		m["hire"] = hire
	case "PENDING", "IN_PROGRESS":
		f, d, err := s.draftOf(ctx, p, tid, id, p.Has("employee.salary.read"))
		if err != nil {
			return nil, err
		}
		if f != nil {
			d["forms"] = f
			m["draft"] = d
		}
	}
	return json.Marshal(m)
}

func (s *SelectionService) TakeHire(ctx context.Context, p *domain.Principal, ip, id string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(id) {
		return domain.ErrValidation
	}
	return s.Repo.HireTake(ctx, meta(p, ip), tid, id)
}

// decryptFields reemplaza las claves indicadas ("<x>Enc", base64) por "<x>" descifrado, a cualquier profundidad.
func decryptFields(c ports.Cipher, doc json.RawMessage, keys ...string) json.RawMessage {
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return doc
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if contains(keys, k) {
					plain := ""
					if enc, ok := val.(string); ok && enc != "" {
						if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
							plain, _ = c.Decrypt(b)
						}
					}
					delete(t, k)
					t[strings.TrimSuffix(k, "Enc")] = plain
					continue
				}
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	out, err := json.Marshal(v)
	if err != nil {
		return doc
	}
	return out
}

// AssignHire: el administrador reasigna la solicitud a otro gestor (en proceso o ya confirmada).
func (s *SelectionService) AssignHire(ctx context.Context, p *domain.Principal, ip, id, userID string) error {
	tid, err := tenantOf(p)
	if err != nil {
		return err
	}
	if !validUUID(id) || !validUUID(userID) {
		return domain.ErrValidation
	}
	return s.Repo.HireAssign(ctx, meta(p, ip), tid, id, userID)
}

// Assignees: gestores a los que se puede asignar una solicitud.
func (s *SelectionService) Assignees(ctx context.Context, p *domain.Principal) ([]json.RawMessage, error) {
	tid, err := tenantOf(p)
	if err != nil {
		return nil, err
	}
	docs, err := s.Repo.HireAssignees(ctx, meta(p, ""), tid)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(docs))
	for _, d := range docs {
		out = append(out, decryptNames(s.Cipher, d))
	}
	return out, nil
}
