package app

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
)

// TestGoldenWebhook rigioca su WebhookHandler + Updater le richieste fatte
// al webhook server di python-telegram-bot (testdata/webhook_golden.json):
// stesso status e stessi update elaborati, nello stesso ordine.
func TestGoldenWebhook(t *testing.T) {
	raw, err := os.ReadFile("testdata/webhook_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Token  string `json:"token"`
		Secret string `json:"secret"`
		Cases  []struct {
			Name          string  `json:"name"`
			RawRequestB64 string  `json:"raw_request_b64"`
			Status        int     `json:"status"`
			Queued        []int64 `json:"queued"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Token != testToken || golden.Secret != testSecret {
		t.Fatalf("token/secret del golden diversi da quelli della fixture")
	}

	f := newWebhookFixture(t, nil)
	var wantQueued []int64
	for _, c := range golden.Cases {
		reqBytes, err := base64.StdEncoding.DecodeString(c.RawRequestB64)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqBytes)))
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != c.Status {
			t.Errorf("%s: status %d; atteso %d", c.Name, rec.Code, c.Status)
		}
		wantQueued = append(wantQueued, c.Queued...)
	}

	f.stop()
	if got := f.recorder.processed(); !slices.Equal(got, wantQueued) {
		t.Errorf("update elaborati = %v, attesi %v", got, wantQueued)
	}
}
