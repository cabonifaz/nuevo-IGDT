package app

import "testing"

// Solo se aceptan endpoints https públicos (los del servicio push del navegador).
func TestValidEndpoint(t *testing.T) {
	for e, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":              true,
		"https://updates.push.services.mozilla.com/wpush/v2/x": true,
		"http://fcm.googleapis.com/fcm/send/abc":               false,
		"https://localhost/push":                               false,
		"https://127.0.0.1/push":                               false,
		"https://10.0.0.5/push":                                false,
		"https://db.internal/push":                             false,
		"nota-url":                                             false,
	} {
		if validEndpoint(e) != want {
			t.Errorf("%s: esperado %v", e, want)
		}
	}
}
