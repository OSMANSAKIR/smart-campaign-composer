package content

import "testing"

func TestHash_StableAndBoundarySafe_B3(t *testing.T) {
	a := Content{SMSText: "ab", EmailSubject: "c", EmailBody: "d"}
	if Hash(a) != Hash(a) || len(Hash(a)) != 64 {
		t.Fatal("hash must be deterministic sha256 hex")
	}
	for name, other := range map[string]Content{
		"sms changed":       {SMSText: "ab!", EmailSubject: "c", EmailBody: "d"},
		"subject changed":   {SMSText: "ab", EmailSubject: "c!", EmailBody: "d"},
		"body changed":      {SMSText: "ab", EmailSubject: "c", EmailBody: "d!"},
		"boundary shifted":  {SMSText: "a", EmailSubject: "bc", EmailBody: "d"},
		"fields rotated":    {SMSText: "c", EmailSubject: "ab", EmailBody: "d"},
		"empty vs not":      {},
		"newline in a text": {SMSText: "ab", EmailSubject: "c", EmailBody: "d\n"},
	} {
		if Hash(a) == Hash(other) {
			t.Errorf("%s: hash collision", name)
		}
	}
}

func TestLocalizeFidelity_B3(t *testing.T) {
	src := Content{SMSText: "Merhaba {{first_name}}, %30 indirim!", EmailSubject: "{{first_name}}, sepetin bekliyor",
		EmailBody: "Merhaba {{first_name}},\n\nSepetin {{city}} için bekliyor."}
	ok := Content{SMSText: "Hi {{first_name}}, 30% off!", EmailSubject: "{{first_name}}, your cart is waiting",
		EmailBody: "Hi {{first_name}},\n\nYour cart is waiting for {{city}}."}
	tests := []struct {
		name string
		mod  func(c *Content)
		want string // issue code; "" = no issue
	}{
		{"faithful adaptation", func(*Content) {}, ""},
		{"variable dropped", func(c *Content) { c.SMSText = "Hi, 30% off!" }, "variable_mismatch"},
		{"variable added", func(c *Content) { c.EmailSubject = "{{first_name}}, {{city}} cart" }, "variable_mismatch"},
		{"variable renamed", func(c *Content) { c.SMSText = "Hi {{ad}}, 30% off!" }, "variable_mismatch"},
		{"spacing inside braces is the same variable", func(c *Content) { c.SMSText = "Hi {{ first_name }}, 30% off!" }, ""},
		{"new link", func(c *Content) { c.EmailBody += "\n\nhttps://example.com/x" }, "new_url"},
		{"new bare domain", func(c *Content) { c.SMSText = "Hi {{first_name}}, shop.com 30% off" }, "new_url"},
		{"new email address", func(c *Content) { c.EmailBody += "\n\nwrite to a@b.com" }, "new_email"},
		{"new phone", func(c *Content) { c.SMSText = "Hi {{first_name}}, call 0850 123 45 67" }, "new_phone"},
		{"unchanged source", func(c *Content) { *c = src }, "not_adapted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ok
			tt.mod(&got)
			issues := LocalizeFidelity(src, got)
			if tt.want == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %+v, want none", issues)
				}
				return
			}
			for _, is := range issues {
				if is.Code == tt.want {
					return
				}
			}
			t.Fatalf("issues = %+v, want %s", issues, tt.want)
		})
	}
	t.Run("a link the source already has may stay", func(t *testing.T) {
		s := src
		s.EmailBody += "\n\nhttps://example.com/c"
		d := ok
		d.EmailBody += "\n\nhttps://example.com/c"
		if issues := LocalizeFidelity(s, d); len(issues) != 0 {
			t.Fatalf("issues = %+v", issues)
		}
	})
}
