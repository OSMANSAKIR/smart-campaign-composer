package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
)

func TestHTTPClient_SendsIdempotencyKeyAndPayload_RSND3(t *testing.T) {
	var gotKey, gotPath, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath, gotCT = r.Header.Get("Idempotency-Key"), r.URL.Path, r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"provider_message_id":"pm_abc","accepted_at":"x"}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL+"/", time.Second) // trailing slash tolerated
	defer c.Close()

	res, err := c.Send(context.Background(), SendRequest{Channel: Email, IdempotencyKey: "msg-1", To: "a@b.co",
		Subject: "S", HTML: "<p>h</p>", Text: "t", CampaignID: "camp", UserID: 7, MessageID: "msg-1"})
	if err != nil || res.Status != 200 || res.ProviderMessageID != "pm_abc" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotKey != "msg-1" || gotPath != "/v1/email" || gotCT != "application/json" {
		t.Errorf("key=%q path=%q content-type=%q", gotKey, gotPath, gotCT)
	}
	for k, want := range map[string]any{"to": "a@b.co", "subject": "S", "html": "<p>h</p>", "text": "t", "campaign_id": "camp", "user_id": float64(7), "message_id": "msg-1"} {
		if gotBody[k] != want {
			t.Errorf("email payload %s = %v, want %v", k, gotBody[k], want)
		}
	}

	gotBody = nil // decoding into an existing map would merge with the email payload
	if _, err := c.Send(context.Background(), SendRequest{Channel: SMS, IdempotencyKey: "msg-2", To: "+905321234567", Text: "hi"}); err != nil || gotPath != "/v1/sms" {
		t.Errorf("sms path = %q err=%v", gotPath, err)
	}
	if _, ok := gotBody["subject"]; ok {
		t.Errorf("sms payload carries email fields: %v", gotBody)
	}
}

func TestHTTPClient_ClassifiesTransportFailures_RSND4(t *testing.T) {
	prov := mockprovider.New(config.ProviderFaults{HangRate: 1, Rate429: 0, Seed: 5}, metrics.New())
	srv := httptest.NewServer(prov.Handler())
	defer srv.Close()
	defer prov.Close()

	c := NewHTTPClient(srv.URL, 60*time.Millisecond)
	defer c.Close()
	res, err := c.Send(context.Background(), SendRequest{Channel: SMS, IdempotencyKey: "k", To: "+905321234567", Text: "x"})
	if err == nil || res.Status != 0 || !IsTimeout(err) || ResponseCode(res.Status, err) != "timeout" {
		t.Fatalf("hang: res=%+v err=%v, want a timeout", res, err)
	}
	if d := DefaultRetryPolicy.Classify(res.Status, res.Header, err); d.Class != ClassAmbiguous {
		t.Fatalf("class = %s", d.Class)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close() // nothing listens there any more
	c2 := NewHTTPClient(url, time.Second)
	defer c2.Close()
	res, err = c2.Send(context.Background(), SendRequest{Channel: SMS, IdempotencyKey: "k", To: "+905321234567", Text: "x"})
	if err == nil || IsTimeout(err) || ResponseCode(res.Status, err) != "conn_error" {
		t.Fatalf("refused: res=%+v err=%v, want conn_error", res, err)
	}
}

func TestHTTPClient_ReturnsRetryAfterOn429_RSND4(t *testing.T) {
	prov := mockprovider.New(config.ProviderFaults{Rate429: 1, RetryAfterS: 7, Seed: 5}, metrics.New())
	srv := httptest.NewServer(prov.Handler())
	defer srv.Close()
	defer prov.Close()
	c := NewHTTPClient(srv.URL, time.Second)
	defer c.Close()
	res, err := c.Send(context.Background(), SendRequest{Channel: SMS, IdempotencyKey: "k", To: "+905321234567", Text: "x"})
	if err != nil || res.Status != 429 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if d := DefaultRetryPolicy.Classify(res.Status, res.Header, err); d.Class != ClassThrottled || d.RetryAfter != 7*time.Second {
		t.Fatalf("decision %+v, want throttled with 7 s", d)
	}
}

func TestRenderMessage_RCNT3(t *testing.T) {
	tmpl := Template{
		SMSText:      "Merhaba {{ first_name }}, {{city}}!",
		EmailSubject: "{{first_name}} için fırsat",
		EmailBody:    "Selam {{first_name}},\r\n\r\nİlk satır\nikinci satır\n\n\n\nSon & <b>paragraf</b>",
	}
	tests := []struct {
		name string
		ch   Channel
		r    Recipient
		want Rendered
		long bool
	}{
		{"sms with spaces inside the braces", SMS, Recipient{Phone: "+905321234567", FirstName: "Ayşe", City: "Bursa", Locale: "tr"},
			Rendered{To: "+905321234567", Text: "Merhaba Ayşe, Bursa!"}, false},
		{"sms fallback for empty name, tr", SMS, Recipient{Phone: "+9", FirstName: "  ", City: "Bursa", Locale: "tr"},
			Rendered{To: "+9", Text: "Merhaba Değerli müşterimiz, Bursa!"}, false},
		{"sms fallback for empty name, en", SMS, Recipient{Phone: "+9", FirstName: "", City: "", Locale: "en"},
			Rendered{To: "+9", Text: "Merhaba Valued customer, Türkiye!"}, false},
		{"sms value is not rescanned", SMS, Recipient{Phone: "+9", FirstName: "{{city}}", City: "Bursa", Locale: "tr"},
			Rendered{To: "+9", Text: "Merhaba {{city}}, Bursa!"}, false},
		{"sms control characters stripped", SMS, Recipient{Phone: "+9", FirstName: "A\nB\x00", City: "X", Locale: "tr"},
			Rendered{To: "+9", Text: "Merhaba AB, X!"}, false},
		{"email html is escaped, paragraphs and breaks", Email, Recipient{Email: "a@b.co", FirstName: "<i>Z</i>", City: "X", Locale: "tr"},
			Rendered{To: "a@b.co", Subject: "<i>Z</i> için fırsat",
				Text: "Selam <i>Z</i>,\r\n\r\nİlk satır\nikinci satır\n\n\n\nSon & <b>paragraf</b>",
				HTML: "<!doctype html><html><body><p>Selam &lt;i&gt;Z&lt;/i&gt;,</p>\n<p>İlk satır<br>\nikinci satır</p>\n<p>Son &amp; &lt;b&gt;paragraf&lt;/b&gt;</p></body></html>"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, long := RenderMessage(tc.ch, tmpl, tc.r)
			if got != tc.want || long != tc.long {
				t.Fatalf("got  %+v long=%v\nwant %+v long=%v", got, long, tc.want, tc.long)
			}
		})
	}

	t.Run("sms limit counts runes, not bytes", func(t *testing.T) {
		at160 := Template{SMSText: strings.Repeat("ş", 160)}
		if _, long := RenderMessage(SMS, at160, Recipient{}); long {
			t.Error("160 runes (320 bytes) flagged too long")
		}
		at161 := Template{SMSText: strings.Repeat("ş", 161)}
		if _, long := RenderMessage(SMS, at161, Recipient{}); !long {
			t.Error("161 runes not flagged")
		}
		withVar := Template{SMSText: strings.Repeat("a", 150) + "{{first_name}}"}
		if _, long := RenderMessage(SMS, withVar, Recipient{FirstName: "Abcdefghijklmnopqrst"}); !long {
			t.Error("150 + a 20-rune name is 170 runes and must be flagged")
		}
	})
}
