package campaign

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// contentB is a second, different angle for the same campaign.
var contentB = content.Content{
	SMSText:      "{{first_name}}, son fırsat: sepetindeki ürünlerde %30 indirim bitmeden al!",
	EmailSubject: "{{first_name}}, indirim bitiyor",
	EmailBody:    "Merhaba {{first_name}},\n\nSepetindeki ürünlerde %30 indirim yarın bitiyor.",
}

// everyoneCampaign is a campaign targeting all users with content version 1 variant A saved through the
// service; B is inserted with SQL (the content storage of variants belongs to another builder).
func everyoneCampaign(t *testing.T, svc *Service, a content.Content) (Campaign, int) {
	t.Helper()
	ctx := context.Background()
	c, err := svc.Create(ctx, "split test brief")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := segment.ParseRule([]byte(`{"groups":[]}`))
	if c, err = svc.PutSegment(ctx, c.ID, all); err != nil {
		t.Fatal(err)
	}
	cv, err := svc.SaveContent(ctx, c.ID, a, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c, cv.Version
}

// addVariantB stores variant B of a version.
func addVariantB(t *testing.T, pool *pgxpool.Pool, id string, version int, b content.Content) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO campaign_content
			(campaign_id, version, variant, label, sms_text, email_subject, email_body, source, validation)
		SELECT campaign_id, version, 'b', 'B', $3, $4, $5, source, validation
		FROM campaign_content WHERE campaign_id = $1 AND version = $2 AND variant = 'a'`,
		id, version, b.SMSText, b.EmailSubject, b.EmailBody); err != nil {
		t.Fatal(err)
	}
}

// splitOf recomputes the documented split outside Postgres: first byte of md5(campaign_id ':' user_id), parity.
func splitOf(campaignID string, userID int64) string {
	sum := md5.Sum([]byte(campaignID + ":" + strconv.FormatInt(userID, 10)))
	if sum[0]%2 == 0 {
		return "a"
	}
	return "b"
}

func TestApprove_SplitDeterministicAndBalanced_B2(t *testing.T) {
	svc, pool := newService(t, 5000)
	ctx := context.Background()
	c, v := everyoneCampaign(t, svc, validContent)
	addVariantB(t, pool, c.ID, v, contentB)
	res, err := svc.ApproveWith(ctx, c.ID, "ab-1", []string{"sms", "email"}, v, ApproveOptions{Variant: "ab"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Campaign.SendVariant == nil || *res.Campaign.SendVariant != "ab" {
		t.Fatalf("send_variant %v", res.Campaign.SendVariant)
	}

	rows, err := pool.Query(ctx, `SELECT user_id, channel, variant FROM messages WHERE campaign_id = $1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	perUser := map[int64]string{}
	count := map[string]map[string]int{"sms": {}, "email": {}}
	for rows.Next() {
		var uid int64
		var ch, variant string
		if err := rows.Scan(&uid, &ch, &variant); err != nil {
			t.Fatal(err)
		}
		if want := splitOf(c.ID, uid); variant != want {
			t.Fatalf("user %d on %s: stored %s, md5 formula says %s", uid, ch, variant, want)
		}
		if prev, ok := perUser[uid]; ok && prev != variant {
			t.Fatalf("user %d got %s and %s across channels", uid, prev, variant)
		}
		perUser[uid] = variant
		count[ch][variant]++
	}
	rows.Close()
	for ch, m := range count {
		total := m["a"] + m["b"]
		if total < 2500 {
			t.Fatalf("%s: only %d messages", ch, total)
		}
		if share := float64(m["b"]) / float64(total); share < 0.45 || share > 0.55 {
			t.Fatalf("%s: variant B share %.3f outside 45..55 %% (%v)", ch, share, m)
		}
		if res.Enqueued[ch] != total {
			t.Fatalf("%s: enqueued %d, rows %d", ch, res.Enqueued[ch], total)
		}
	}

	// The progress snapshot reports the same split and it adds up per channel.
	p, err := svc.Progress(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for ch, cp := range p.Channels {
		if cp.Variants["a"] == nil || cp.Variants["b"] == nil {
			t.Fatalf("%s variants %+v", ch, cp.Variants)
		}
		if cp.Variants["a"].Total != count[ch]["a"] || cp.Variants["b"].Total != count[ch]["b"] {
			t.Fatalf("%s: progress %+v vs rows %v", ch, cp.Variants, count[ch])
		}
		if cp.Variants["a"].Total+cp.Variants["b"].Total != cp.Total {
			t.Fatalf("%s: variant totals do not add up to %d", ch, cp.Total)
		}
	}
}

func TestApprove_SendModes_B2(t *testing.T) {
	for _, tc := range []struct{ variant, want string }{{"", "a"}, {"a", "a"}, {"b", "b"}} {
		t.Run("variant "+tc.variant, func(t *testing.T) {
			svc, pool := newService(t, 300)
			ctx := context.Background()
			c, v := everyoneCampaign(t, svc, validContent)
			addVariantB(t, pool, c.ID, v, contentB)
			res, err := svc.ApproveWith(ctx, c.ID, "k", []string{"sms", "email"}, v, ApproveOptions{Variant: tc.variant})
			if err != nil {
				t.Fatal(err)
			}
			if got := *res.Campaign.SendVariant; got != tc.want {
				t.Fatalf("send_variant %s, want %s", got, tc.want)
			}
			var other int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE campaign_id = $1 AND variant <> $2`, c.ID, tc.want).Scan(&other); err != nil {
				t.Fatal(err)
			}
			if other != 0 {
				t.Fatalf("%d messages with a variant other than %s", other, tc.want)
			}
		})
	}
}

func TestApprove_VariantMissing_B2(t *testing.T) {
	svc, _ := newService(t, 300)
	ctx := context.Background()
	c, v := everyoneCampaign(t, svc, validContent) // no variant B
	for _, variant := range []string{"b", "ab"} {
		_, err := svc.ApproveWith(ctx, c.ID, "k-"+variant, []string{"sms"}, v, ApproveOptions{Variant: variant})
		if codeOf(err) != "variant_missing" {
			t.Fatalf("variant %s: %v", variant, err)
		}
	}
	if _, err := svc.ApproveWith(ctx, c.ID, "k-bad", []string{"sms"}, v, ApproveOptions{Variant: "c"}); codeOf(err) != "bad_request" {
		t.Fatalf("unknown variant: %v", err)
	}
	// Nothing was enqueued or frozen by the failed attempts, so A still goes out.
	res, err := svc.ApproveWith(ctx, c.ID, "k-a", []string{"sms"}, v, ApproveOptions{Variant: "a"})
	if err != nil || res.Campaign.Status != StatusSending {
		t.Fatalf("variant a after failed attempts: %v %+v", err, res.Campaign.Status)
	}
}

func TestApprove_InvalidBBlocksABNotA_B2(t *testing.T) {
	svc, pool := newService(t, 300)
	ctx := context.Background()
	c, v := everyoneCampaign(t, svc, validContent)
	badB := contentB
	badB.SMSText = strings.Repeat("ş", 170)
	addVariantB(t, pool, c.ID, v, badB)

	for _, variant := range []string{"b", "ab"} {
		_, err := svc.ApproveWith(ctx, c.ID, "k-"+variant, []string{"sms"}, v, ApproveOptions{Variant: variant})
		if codeOf(err) != "content_invalid" {
			t.Fatalf("variant %s with invalid B: %v", variant, err)
		}
		var ce *Error
		if !asError(err, &ce) || len(ce.Details) == 0 || !strings.HasPrefix(ce.Details[0].Path, "variants.b.") {
			t.Fatalf("variant %s: details %+v, want paths under variants.b.", variant, ce)
		}
	}
	// B is only invalid for SMS: an email-only send of both variants is fine, and so is A alone on SMS.
	if _, err := svc.ApproveWith(ctx, c.ID, "k-email", []string{"email"}, v, ApproveOptions{Variant: "ab"}); err != nil {
		t.Fatalf("email-only ab with B invalid for sms only: %v", err)
	}
	svc2, pool2 := newService(t, 300)
	c2, v2 := everyoneCampaign(t, svc2, validContent)
	addVariantB(t, pool2, c2.ID, v2, badB)
	if _, err := svc2.ApproveWith(ctx, c2.ID, "k-a", []string{"sms"}, v2, ApproveOptions{Variant: "a"}); err != nil {
		t.Fatalf("variant a must not be blocked by an invalid B: %v", err)
	}
}

func TestProgress_PerVariantCounts_B2(t *testing.T) {
	svc, pool := newService(t, 60)
	ctx := context.Background()
	id := sendingCampaign(t, pool, map[string][]string{
		"sms":   {"queued", "sent", "sent", "failed", "cancelled", "skipped_no_consent", "sent"},
		"email": {"sent", "cancelled"},
	})
	// Give every other message variant b.
	if _, err := pool.Exec(ctx, `UPDATE messages SET variant = 'b' WHERE campaign_id = $1 AND user_id % 2 = 0`, id); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Progress(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for ch, cp := range p.Channels {
		var sent, failed, cancelled, skipped, total int
		for _, vp := range cp.Variants {
			sent, failed, cancelled, skipped, total = sent+vp.Sent, failed+vp.Failed, cancelled+vp.Cancelled, skipped+vp.SkippedNoConsent, total+vp.Total
		}
		if sent != cp.Sent || failed != cp.Failed || cancelled != cp.Cancelled || skipped != cp.SkippedNoConsent || total != cp.Total {
			t.Fatalf("%s: variants %s do not add up to the channel %+v", ch, fmt.Sprint(cp.Variants), cp)
		}
	}
	if sms := p.Channels["sms"]; sms.Cancelled != 1 || sms.Sent != 3 || sms.Total != 7 || len(sms.Variants) != 2 {
		t.Fatalf("sms %+v", sms)
	}
	items, _, err := svc.Messages(ctx, id, "cancelled", "", 10, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("cancelled messages: %v %v", items, err)
	}
	for _, m := range items {
		if m.Variant != "a" && m.Variant != "b" {
			t.Fatalf("message variant %q", m.Variant)
		}
	}
}

func asError(err error, target **Error) bool {
	return errors.As(err, target)
}
