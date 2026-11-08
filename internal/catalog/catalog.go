// Package catalog is the single source of truth for the segment DSL: fields, types, operators, limits,
// personalization variables and content limits. The prompt text, the wire schemas, the validator, the SQL
// compiler and the UI labels (GET /api/catalog) are all derived from here (docs/contracts/segment-dsl.md).
package catalog

import "slices"

// Type is a field's value type.
type Type string

const (
	Number Type = "number"
	Enum   Type = "enum"
	Bool   Type = "bool"
	Date   Type = "date"
)

// Op is a DSL operator.
type Op string

const (
	OpEq                Op = "eq"
	OpNeq               Op = "neq"
	OpGt                Op = "gt"
	OpGte               Op = "gte"
	OpLt                Op = "lt"
	OpLte               Op = "lte"
	OpBetween           Op = "between"
	OpIn                Op = "in"
	OpNotIn             Op = "not_in"
	OpIsTrue            Op = "is_true"
	OpIsFalse           Op = "is_false"
	OpWithinLastDays    Op = "within_last_days"
	OpNotWithinLastDays Op = "not_within_last_days"
	OpBefore            Op = "before"
	OpAfter             Op = "after"
	OpIsNull            Op = "is_null"
	OpIsNotNull         Op = "is_not_null"
)

// OpsByType lists the operators allowed for each type.
var OpsByType = map[Type][]Op{
	Number: {OpEq, OpNeq, OpGt, OpGte, OpLt, OpLte, OpBetween},
	Enum:   {OpEq, OpNeq, OpIn, OpNotIn},
	Bool:   {OpIsTrue, OpIsFalse},
	Date:   {OpWithinLastDays, OpNotWithinLastDays, OpBefore, OpAfter, OpIsNull, OpIsNotNull},
}

// Label is a UI label in both supported languages.
type Label struct {
	EN string `json:"en"`
	TR string `json:"tr"`
}

// Field describes one segmentable attribute of a user.
type Field struct {
	Name        string   `json:"name"`
	Type        Type     `json:"type"`
	SQL         string   `json:"-"` // whitelisted SQL expression over users u; never model-controlled
	Label       Label    `json:"label"`
	Description string   `json:"description"` // English; used verbatim in the prompt
	Values      []string `json:"values,omitempty"`
	Min         float64  `json:"min,omitempty"`
	Max         float64  `json:"max,omitempty"`
	Integer     bool     `json:"integer,omitempty"`
	Nullable    bool     `json:"nullable"`
	Unit        string   `json:"unit,omitempty"`
	Ops         []Op     `json:"ops"`
}

// Cities are the seeded cities; the seed generator uses this list too.
var Cities = []string{"İstanbul", "Ankara", "İzmir", "Bursa", "Antalya", "Adana", "Konya", "Gaziantep", "Kocaeli", "Eskişehir", "Trabzon", "Kayseri"}

// LoyaltyTiers and Categories are the other seeded enums.
var (
	LoyaltyTiers = []string{"bronze", "silver", "gold", "platinum"}
	Categories   = []string{"electronics", "fashion", "home", "beauty", "sports", "books", "grocery", "toys"}
	Locales      = []string{"tr", "en"}
)

var fields = []Field{
	{Name: "city", Type: Enum, SQL: "u.city", Label: Label{"City", "Şehir"}, Description: "City the user lives in.", Values: Cities},
	{Name: "locale", Type: Enum, SQL: "u.locale", Label: Label{"Language", "Dil"}, Description: "Preferred language of the user.", Values: Locales},
	{Name: "age", Type: Number, SQL: "EXTRACT(YEAR FROM age(u.birth_date))::int", Label: Label{"Age", "Yaş"}, Description: "Age in whole years.", Min: 18, Max: 100, Integer: true, Unit: "years"},
	{Name: "loyalty_tier", Type: Enum, SQL: "u.loyalty_tier", Label: Label{"Loyalty tier", "Sadakat seviyesi"}, Description: "Loyalty program tier.", Values: LoyaltyTiers},
	{Name: "favorite_category", Type: Enum, SQL: "u.favorite_category", Label: Label{"Favorite category", "Favori kategori"}, Description: "Product category the user buys or views most. May be unknown (null).", Values: Categories, Nullable: true},
	{Name: "signup_at", Type: Date, SQL: "u.signup_at", Label: Label{"Signed up", "Üyelik tarihi"}, Description: "When the user created the account."},
	{Name: "last_login_at", Type: Date, SQL: "u.last_login_at", Label: Label{"Last login", "Son giriş"}, Description: "Last login time. Null if never logged in after signup.", Nullable: true},
	{Name: "last_purchase_at", Type: Date, SQL: "u.last_purchase_at", Label: Label{"Last purchase", "Son alışveriş"}, Description: "Time of the most recent purchase. Null if the user never purchased.", Nullable: true},
	{Name: "last_cart_add_at", Type: Date, SQL: "u.last_cart_add_at", Label: Label{"Last add to cart", "Son sepete ekleme"}, Description: "Time the user last added a product to the cart. Null if never.", Nullable: true},
	{Name: "purchase_count", Type: Number, SQL: "u.purchase_count", Label: Label{"Number of purchases", "Alışveriş sayısı"}, Description: "Total number of purchases ever.", Min: 0, Max: 100000, Integer: true},
	{Name: "total_spent", Type: Number, SQL: "u.total_spent", Label: Label{"Total spent", "Toplam harcama"}, Description: "Total amount spent ever, in Turkish lira.", Min: 0, Max: 10000000, Unit: "TRY"},
	{Name: "cart_abandoned", Type: Bool, SQL: "u.cart_abandoned", Label: Label{"Abandoned cart", "Terk edilmiş sepet"}, Description: "True when the user added to cart and has not purchased since that cart add."},
}

func init() {
	for i := range fields {
		fields[i].Ops = OpsByType[fields[i].Type]
	}
}

// Fields returns all fields in catalog order. Callers must not modify the result.
func Fields() []Field { return fields }

// Lookup finds a field by name.
func Lookup(name string) (Field, bool) {
	i := slices.IndexFunc(fields, func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	return fields[i], true
}

// FieldNames returns the names of all fields of type t, in catalog order.
func FieldNames(t Type) []string {
	var out []string
	for _, f := range fields {
		if f.Type == t {
			out = append(out, f.Name)
		}
	}
	return out
}

// Limits enforced by the segment validator (segment-dsl.md §4).
const (
	MaxGroups             = 5
	MaxConditionsPerGroup = 10
	MaxConditionsTotal    = 20
	MaxListItems          = 50
	MinDays               = 1
	MaxDays               = 3650
	MaxStringRunes        = 100
	MaxNoteItems          = 10
	MaxNoteRunes          = 300
	MaxExplanationRunes   = 1000
	MinDate               = "2000-01-01"
	MaxDate               = "2100-12-31"
)

// Personalization and content limits (KICKOFF §6.5, need.md N-ORC-005).
var Variables = []string{"first_name", "city"}

const (
	MaxBriefRunes        = 4000
	SMSMaxRunes          = 160
	EmailSubjectMaxRunes = 80
	EmailBodyMaxRunes    = 5000
	MaxVariableRunes     = 20
)

// Channels are the delivery channels.
var Channels = []string{"sms", "email"}

// Document is the GET /api/catalog response.
type Document struct {
	Fields        []Field        `json:"fields"`
	Operators     map[Type][]Op  `json:"operators"`
	Limits        map[string]any `json:"limits"`
	Variables     []string       `json:"variables"`
	ContentLimits map[string]int `json:"content_limits"`
	Channels      []string       `json:"channels"`
}

// Doc builds the catalog document for the API and the UI.
func Doc() Document {
	return Document{
		Fields:    fields,
		Operators: OpsByType,
		Limits: map[string]any{
			"max_groups": MaxGroups, "max_conditions_per_group": MaxConditionsPerGroup,
			"max_conditions_total": MaxConditionsTotal, "max_list_items": MaxListItems,
			"min_days": MinDays, "max_days": MaxDays, "max_string_runes": MaxStringRunes,
			"min_date": MinDate, "max_date": MaxDate,
		},
		Variables: Variables,
		ContentLimits: map[string]int{
			"sms_text": SMSMaxRunes, "email_subject": EmailSubjectMaxRunes, "email_body": EmailBodyMaxRunes,
			"variable_value": MaxVariableRunes,
		},
		Channels: Channels,
	}
}
