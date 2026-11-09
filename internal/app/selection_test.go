package app

import (
	"testing"
	"time"
)

// La cobertura sigue la regla de BDT (confirmados sin renuncia) y el tablero recibe la entrada a la etapa de cada
// postulante en proceso para calcular los días sin movimiento.
func TestNormalizeRequirementCoverage(t *testing.T) {
	const v1, v2 = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	q := &BDTRequirement{ID: "33333333-3333-4333-8333-333333333333", Code: "RQ-1", Title: "Analistas", UpdatedAt: time.Now(),
		Alerts: &bdtAlerts{Enabled: true, WarnDays: 5, CriticalDays: 3}}
	q.StatusIn.Code, q.StatusIn.Label, q.StatusIn.Group = "IN_SELECTION", "En selección", "OPEN"
	q.Vacancies = []bdtVacancy{{ID: v1, Profile: bdtRef{Label: "Analista"}, Quantity: 2, Covered: 9}, {ID: v2, Profile: bdtRef{Label: "QA"}, Quantity: 1}}
	cand := func(n int, vac, stage, at string, resigned bool) bdtCandidate {
		return bdtCandidate{ID: "4444444" + string(rune('0'+n)) + "-4444-4444-8444-444444444444", VacancyID: vac, Name: "x",
			Stage: bdtStage{Code: stage}, StageAt: at, Resigned: resigned}
	}
	q.Candidates = []bdtCandidate{
		cand(1, v1, "CONFIRMED", "2026-11-01T10:00:00.000Z", false),
		cand(2, v1, "CONFIRMED", "2026-11-02T10:00:00.000Z", true),
		cand(3, v2, "INTERVIEW", "2026-11-03T10:00:00.000Z", false),
		cand(4, v2, "OBSERVED", "2026-11-04T10:00:00.000Z", true),
	}
	w, ok := normalizeRequirement(q)
	if !ok {
		t.Fatal("snapshot válido rechazado")
	}
	if w.Covered != 1 || w.Vacancies != 3 || w.Candidates != 4 {
		t.Fatalf("cobertura %d/%d, postulantes %d", w.Covered, w.Vacancies, w.Candidates)
	}
	if q.Vacancies[0].Covered != 1 || q.Vacancies[1].Covered != 0 {
		t.Fatalf("cobertura por vacante: %d, %d", q.Vacancies[0].Covered, q.Vacancies[1].Covered)
	}
	if len(q.InProcessAt) != 1 || q.InProcessAt[0] != "2026-11-03T10:00:00.000Z" {
		t.Fatalf("en proceso: %v", q.InProcessAt)
	}
	if q.Alerts != nil {
		t.Fatal("días de alerta inválidos (crítico <= atención) deben descartarse")
	}
}
