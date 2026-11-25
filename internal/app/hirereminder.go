package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cabonifaz/nuevo-IGDT/internal/app/ports"
	"github.com/cabonifaz/nuevo-IGDT/internal/domain"
)

// Recordatorios de solicitudes de ingreso pendientes (V0019), desde las 8:00 (hora de Lima):
//   - cada día, una notificación (y push) a cada gestor con sus pendientes, por vencer y vencidas;
//   - cada 2 o 3 días (según el tenant), un correo a cada gestor con el detalle.
// Las solicitudes de un cliente van a sus dos gestores; las de clientes sin gestores, a los administradores.
// Varias instancias de la API no duplican avisos: cada corrida se reserva en la base (sp_hire_reminder_claim).

const reminderHour = 8

// RunReminders revisa cada 10 minutos si toca enviar los recordatorios, hasta que se cancele ctx.
func (s *SelectionService) RunReminders(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		s.reminderTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *SelectionService) reminderTick(ctx context.Context) {
	now := time.Now().In(limaTZ())
	if now.Hour() < reminderHour {
		return
	}
	tenants, err := s.Repo.HireReminderTenants(ctx)
	if err != nil {
		slog.Warn("recordatorios: no se pudieron leer los tenants", "err", err)
		return
	}
	for _, t := range tenants {
		if ok, err := s.Repo.HireReminderClaim(ctx, t.ID, "DAILY", now, 1); err == nil && ok {
			s.remind(ctx, t, now, false)
		}
		if ok, err := s.Repo.HireReminderClaim(ctx, t.ID, "EMAIL", now, t.ReminderDays); err == nil && ok {
			s.remind(ctx, t, now, true)
		}
	}
}

type reminderUser struct {
	ID     string `json:"id"`
	UserID uint64 `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Locale string `json:"locale"`
	items  []HireAlertItem
}

// remind arma los pendientes de cada gestor y envía la notificación diaria o el correo.
func (s *SelectionService) remind(ctx context.Context, t ports.ReminderTenant, now time.Time, byEmail bool) {
	reqs, admins, err := s.Repo.HirePending(ctx, t.ID)
	if err != nil {
		slog.Warn("recordatorios: no se pudieron leer los pendientes", "tenant", t.Slug, "err", err)
		return
	}
	users := map[uint64]*reminderUser{}
	addTo := func(list []reminderUser, it HireAlertItem) {
		for _, u := range list {
			if users[u.UserID] == nil {
				cp := u
				users[u.UserID] = &cp
			}
			users[u.UserID].items = append(users[u.UserID].items, it)
		}
	}
	var adminList []reminderUser
	for _, d := range admins {
		var u reminderUser
		if json.Unmarshal(decryptFields(s.Cipher, decryptNames(s.Cipher, d), "emailEnc"), &u) == nil {
			adminList = append(adminList, u)
		}
	}
	for _, d := range reqs {
		var x struct {
			ID          string                 `json:"id"`
			ReceivedAt  string                 `json:"receivedAt"`
			Requirement string                 `json:"requirement"`
			StartDate   string                 `json:"startDate"`
			EquipReq    bool                   `json:"equipmentRequired"`
			Approved    []string               `json:"approved"`
			Client      struct{ Label string } `json:"client"`
			Talent      struct {
				FirstName      string `json:"firstName"`
				LastName       string `json:"lastName"`
				SecondLastName string `json:"secondLastName"`
			} `json:"talent"`
			Managers []reminderUser `json:"managers"`
		}
		if json.Unmarshal(decryptFields(s.Cipher, decryptNames(s.Cipher, d), "emailEnc"), &x) != nil {
			continue
		}
		it := HireAlertItem{ID: x.ID, Talent: strings.Join(nonEmpty([]string{x.Talent.FirstName, x.Talent.LastName, x.Talent.SecondLastName}), " "),
			Client: x.Client.Label, RQ: x.Requirement, StartDate: dateOnly(x.StartDate), Due: dueState(x.StartDate, now),
			Pending: pendingForms(x.Approved, x.EquipReq), ReceivedAt: x.ReceivedAt}
		if len(x.Managers) > 0 {
			addTo(x.Managers, it)
		} else {
			addTo(adminList, it)
		}
	}
	for _, u := range users {
		rank := map[string]int{"OVERDUE": 0, "DUE_SOON": 1, "OK": 2}
		sort.SliceStable(u.items, func(i, j int) bool {
			if rank[u.items[i].Due] != rank[u.items[j].Due] {
				return rank[u.items[i].Due] < rank[u.items[j].Due]
			}
			return u.items[i].StartDate < u.items[j].StartDate
		})
		if byEmail {
			s.reminderEmail(ctx, t, u)
		} else {
			s.reminderNotify(ctx, t, u)
		}
	}
}

func countDue(items []HireAlertItem) (dueSoon, overdue int) {
	for _, it := range items {
		switch it.Due {
		case "DUE_SOON":
			dueSoon++
		case "OVERDUE":
			overdue++
		}
	}
	return
}

// reminderNotify: notificación diaria (con push) al gestor.
func (s *SelectionService) reminderNotify(ctx context.Context, t ports.ReminderTenant, u *reminderUser) {
	if s.Notifier == nil {
		return
	}
	due, over := countDue(u.items)
	title := fmt.Sprintf("%d solicitudes de ingreso pendientes", len(u.items))
	if len(u.items) == 1 {
		title = "1 solicitud de ingreso pendiente"
	}
	payload, _ := json.Marshal(map[string]any{"title": title, "pending": len(u.items), "dueSoon": due, "overdue": over})
	in := ports.NotifyInput{ID: uuid.NewString(), Type: domain.NotifHirePending, EntityType: "HIRE_PENDING", EntityID: uuid.NewString(), Payload: payload}
	res, err := s.Notifier.Repo.NotifyUsers(ctx, ports.Meta{}, t.ID, in, ports.AudienceUser, u.UserID, nil)
	if err != nil {
		slog.Warn("recordatorios: no se pudo notificar", "tenant", t.Slug, "err", err)
		return
	}
	n := s.Notifier
	if len(res.Targets) == 0 || n.Push == nil || !n.Push.Enabled() {
		return
	}
	body := fmt.Sprintf("%d por vencer · %d vencidas", due, over)
	go n.deliver(res.ID, res.Targets, func(locale string) pushMessage {
		tt, bb := title, body
		if strings.HasPrefix(locale, "en") {
			tt, bb = fmt.Sprintf("%d pending hire requests", len(u.items)), fmt.Sprintf("%d due soon · %d overdue", due, over)
		}
		return pushMessage{Title: tt, Body: bb, URL: "/" + t.Slug + "/solicitudes", Tag: "hire-pending", ID: res.ID}
	})
}

var formShort = map[string]string{"FMI": "Ingreso (FMI)", "USERS": "Usuarios", "EQUIPMENT": "Equipo"}

// reminderEmail: correo al gestor con sus solicitudes vencidas, por vencer y pendientes.
func (s *SelectionService) reminderEmail(ctx context.Context, t ports.ReminderTenant, u *reminderUser) {
	if s.Mail == nil || u.Email == "" {
		return
	}
	due, over := countDue(u.items)
	subject := fmt.Sprintf("Solicitudes de ingreso pendientes: %d (%d por vencer, %d vencidas)", len(u.items), due, over)
	base := strings.TrimRight(s.AppURL, "/") + "/" + t.Slug + "/solicitudes"
	var b, txt strings.Builder
	fmt.Fprintf(&b, `<div style="%s">`, mailFont)
	first := strings.Fields(u.Name)
	greet := "Hola"
	if len(first) > 0 {
		greet = "Hola " + first[0]
	}
	fmt.Fprintf(&b, "<p>%s,</p><p>Tienes <b>%d</b> solicitudes de ingreso por completar", esc(greet), len(u.items))
	fmt.Fprintf(&txt, "%s,\n\nTienes %d solicitudes de ingreso por completar", greet, len(u.items))
	if over > 0 || due > 0 {
		fmt.Fprintf(&b, `: <b style="color:#B42318">%d vencidas</b> y <b style="color:#B54708">%d por vencer</b> (inician en %d días o menos)`, over, due, DueSoonDays)
		fmt.Fprintf(&txt, ": %d vencidas y %d por vencer", over, due)
	}
	b.WriteString(".</p>")
	txt.WriteString(".\n\n")
	labels := map[string]string{"OVERDUE": "Vencida", "DUE_SOON": "Por vencer", "OK": "Pendiente"}
	colors := map[string]string{"OVERDUE": "#B42318", "DUE_SOON": "#B54708", "OK": "#344054"}
	rows := [][]string{}
	for _, it := range u.items {
		pend := []string{}
		for _, f := range it.Pending {
			pend = append(pend, formShort[f])
		}
		start := "—"
		if it.StartDate != "" {
			start = fmtDate(it.StartDate)
		}
		rows = append(rows, []string{
			fmt.Sprintf(`<b style="color:%s">%s</b>`, colors[it.Due], labels[it.Due]),
			fmt.Sprintf(`<a href="%s/%s">%s</a>`, esc(base), esc(it.ID), esc(it.RQ)), esc(it.Client), esc(it.Talent), esc(start), esc(strings.Join(pend, ", ")),
		})
		fmt.Fprintf(&txt, "- [%s] %s · %s · %s · inicio %s · falta: %s\n", labels[it.Due], it.RQ, it.Client, it.Talent, start, strings.Join(pend, ", "))
	}
	b.WriteString(mailTable("#2F5496", []string{"Estado", "RQ", "Cliente", "Talento", "Inicio", "Fichas por aprobar"}, rows))
	fmt.Fprintf(&b, `<p><a href="%s">Abrir las solicitudes en IGDT</a></p>`, esc(base))
	fmt.Fprintf(&txt, "\nAbrir en IGDT: %s\n", base)
	company := strings.ToUpper(strings.TrimSpace(strings.Split(t.Company, " - ")[0]))
	footer := fmt.Sprintf("Este correo ha sido enviado automáticamente desde %s. Esta es una bandeja DESATENDIDA; por favor, no respondas a este mensaje.", company)
	fmt.Fprintf(&b, `<hr style="border:0;border-top:1px solid #ccc"><p style="font-size:12px;color:#555">%s</p></div>`, esc(footer))
	fmt.Fprintf(&txt, "\n%s\n", footer)
	if err := s.Mail.Send(ctx, ports.Mail{To: []string{u.Email}, Subject: subject, HTML: b.String(), Text: txt.String()}); err != nil && err != ports.ErrNoMailer {
		slog.Warn("recordatorios: no se pudo enviar el correo", "tenant", t.Slug, "err", err)
	}
}
