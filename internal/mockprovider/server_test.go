package mockprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
)

func post(t *testing.T, url, key, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestSend_IdempotentReplayCountsOnce_RRES3(t *testing.T) {
	srv := httptest.NewServer(New(config.ProviderFaults{}, metrics.New()).Handler())
	defer srv.Close()
	body := `{"to":"+905321234567","text":"Merhaba","campaign_id":"c1","user_id":1,"message_id":"m1"}`
	for range 2 {
		if code := post(t, srv.URL+"/v1/sms", "m1", body); code != 200 {
			t.Fatalf("status %d", code)
		}
	}
	if code := post(t, srv.URL+"/v1/sms", "m2", `{"to":"0532","text":"x","campaign_id":"c1","user_id":2}`); code != 400 {
		t.Fatalf("invalid recipient: status %d, want 400", code)
	}
	if code := post(t, srv.URL+"/v1/sms", "", body); code != 400 {
		t.Fatalf("missing key: status %d, want 400", code)
	}

	resp, err := http.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		Channels map[string]ChannelStats `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	sms := stats.Channels["sms"]
	if sms.Accepted != 1 || sms.ReplayedKeys != 1 || sms.DuplicateRecipientDeliveries != 0 {
		t.Fatalf("stats = %+v", sms)
	}
}
