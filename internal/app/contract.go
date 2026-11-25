package app

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

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
