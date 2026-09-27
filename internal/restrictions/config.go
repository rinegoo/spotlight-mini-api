package restrictions

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// RegionNone — ограничения выключены (разработка, заведения вне юрисдикций).
const RegionNone = "none"

// Config — регион заведения и политики по видам ограничений.
//
// Регион включает виды своей юрисдикции с политикой по умолчанию; политику вида
// можно ужесточить (Overrides), но не ослабить ниже обязательного минимума.
type Config struct {
	Regions   []string          // юрисдикции (RU); пусто или none — ограничения выключены
	Overrides map[string]Policy // вид → запрошенная политика
}

// KindPolicy — действующая политика вида (для /api/stats и журнала).
type KindPolicy struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Policy    Policy `json:"policy"`              // действующая
	Requested Policy `json:"requested,omitempty"` // запрошенная, если отличается (ниже минимума)
	Min       Policy `json:"min"`
}

// ParseRegions разбирает «RU» / «RU,BY» / «none».
func ParseRegions(s string) []string {
	var out []string
	for _, r := range strings.Split(s, ",") {
		r = strings.ToUpper(strings.TrimSpace(r))
		if r == "" || r == strings.ToUpper(RegionNone) {
			continue
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// Enabled — задан хотя бы один регион.
func (c Config) Enabled() bool { return len(c.Regions) > 0 }

// applies — вид относится к одному из регионов заведения.
func (c Config) applies(k Kind) bool {
	return slices.Contains(c.Regions, strings.ToUpper(k.Jurisdiction))
}

// Effective — действующая политика вида: off вне регионов; иначе запрошенная
// (или по умолчанию), но не мягче обязательного минимума.
func (c Config) Effective(kind string) Policy {
	k, ok := LookupKind(kind)
	if !ok {
		// Вид неизвестен (запись из чужого источника) — не применяем.
		return PolicyOff
	}
	if !c.applies(k) {
		return PolicyOff
	}
	p := k.Default
	if o, ok := c.Overrides[kind]; ok {
		p = o
	}
	if k.Min.Stricter(p) {
		return k.Min
	}
	return p
}

// Policies — действующие политики всех видов регионов заведения.
func (c Config) Policies() []KindPolicy {
	var out []KindPolicy
	for _, k := range Kinds() {
		if !c.applies(k) {
			continue
		}
		kp := KindPolicy{Kind: k.ID, Title: k.Title, Policy: c.Effective(k.ID), Min: k.Min}
		if o, ok := c.Overrides[k.ID]; ok && o != kp.Policy {
			kp.Requested = o
		}
		out = append(out, kp)
	}
	return out
}

// EnvKey — имя переменной окружения для политики вида: ru.inoagent → RESTRICTIONS_POLICY_RU_INOAGENT.
func EnvKey(kind string) string {
	return "RESTRICTIONS_POLICY_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(kind))
}

// ConfigFromEnv собирает Config: регион и политики видов из переменных окружения.
func ConfigFromEnv(region string, getenv func(string) string) (Config, error) {
	c := Config{Regions: ParseRegions(region), Overrides: map[string]Policy{}}
	for _, k := range Kinds() {
		v := getenv(EnvKey(k.ID))
		if v == "" {
			continue
		}
		p, ok := ParsePolicy(v)
		if !ok {
			return c, fmt.Errorf("%s=%q: want off | label | hide", EnvKey(k.ID), v)
		}
		c.Overrides[k.ID] = p
	}
	for _, kp := range c.Policies() {
		if kp.Requested != "" {
			slog.Warn("restrictions: policy below the legal minimum is not allowed, using the minimum",
				"kind", kp.Kind, "requested", kp.Requested, "effective", kp.Policy)
		}
	}
	return c, nil
}
