// Package restrictions — модуль ограничений контента: официальные реестры и
// ручные списки → указания у песен или скрытие.
//
// Уровни:
//   - restrictions — общее ядро: записи реестров (Entry), ручной список (Manual),
//     сопоставление с каталогом, политики по видам и регионам (Config);
//   - restrictions/ru — российские виды ограничений: формы указаний и
//     обязательные минимумы политик по закону;
//   - restrictions/ru/inoagents, restrictions/ru/extremist — источники данных.
//
// Каждый вид ограничения принадлежит юрисдикции (RU…) и задаёт политику по
// умолчанию и обязательный минимум (например, экстремистские материалы — только
// скрывать). Заведение указывает регион; ослабить политику ниже минимума нельзя.
//
// Автоматически применяются совпадения по псевдонимам физлиц и — для видов с
// AutoText — по тексту записи (название песни в кавычках + исполнитель). Совпадения
// по ФИО (однофамильцы) и названиям организаций — кандидаты на проверку в
// ручном списке. Запись перестаёт действовать с даты исключения из реестра.
package restrictions

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/textnorm"
)

// Policy — что делать с песнями под ограничением. Порядок строгости: off < label < hide.
type Policy string

const (
	PolicyOff   Policy = "off"   // не применять
	PolicyLabel Policy = "label" // показывать с указанием
	PolicyHide  Policy = "hide"  // не показывать в каталоге
)

func (p Policy) rank() int {
	switch p {
	case PolicyLabel:
		return 1
	case PolicyHide:
		return 2
	}
	return 0
}

// Stricter — p строже q.
func (p Policy) Stricter(q Policy) bool { return p.rank() > q.rank() }

// ParsePolicy разбирает политику.
func ParsePolicy(s string) (Policy, bool) {
	switch p := Policy(strings.ToLower(strings.TrimSpace(s))); p {
	case PolicyOff, PolicyLabel, PolicyHide:
		return p, true
	}
	return "", false
}

// Kind — вид ограничения: юрисдикция, форма указания, политики.
type Kind struct {
	ID           string // например "ru.inoagent"
	Jurisdiction string // "RU"
	Title        string // «Иностранный агент»
	// LabelTemplate — форма указания; {name} заменяется наименованием/ФИО из реестра.
	LabelTemplate string
	Default       Policy // политика по умолчанию
	Min           Policy // обязательный минимум по закону (ослабить нельзя)
	// AutoText — совпадения по тексту записи (название + исполнитель) применять
	// сразу, а не отправлять на проверку: для видов, где пропуск опаснее ошибки.
	AutoText bool
}

var (
	kindsMu sync.RWMutex
	kinds   = map[string]Kind{}
)

// RegisterKind регистрирует вид ограничения (вызывается из пакетов юрисдикций).
func RegisterKind(k Kind) {
	kindsMu.Lock()
	defer kindsMu.Unlock()
	kinds[k.ID] = k
}

// LookupKind возвращает зарегистрированный вид.
func LookupKind(id string) (Kind, bool) {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	k, ok := kinds[id]
	return k, ok
}

// Kinds — все зарегистрированные виды, по ID.
func Kinds() []Kind {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	out := make([]Kind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Entry — запись официального реестра (иноагент, экстремистский материал…).
type Entry struct {
	Kind    string     `json:"kind"`
	Subject string     `json:"subject"`           // наименование / ФИО / номер записи как в реестре
	Person  bool       `json:"person,omitempty"`  // физическое лицо
	Aliases []string   `json:"aliases,omitempty"` // псевдонимы (для физлиц — применяются автоматически)
	Name    string     `json:"name,omitempty"`    // ФИО / наименование без псевдонимов и скобок
	Text    string     `json:"text,omitempty"`    // описание материала (для сопоставления по тексту)
	Titles  []string   `json:"titles,omitempty"`  // названия в кавычках из описания
	Since   *time.Time `json:"since,omitempty"`   // дата включения
	Until   *time.Time `json:"until,omitempty"`   // дата исключения
	Source  string     `json:"source,omitempty"`
}

// Active — действует ли запись на момент t.
func (e Entry) Active(t time.Time) bool {
	return e.Until == nil || t.Before(*e.Until)
}

// Match — песня каталога попала под запись.
type Match struct {
	Entry  *Entry
	Artist string // исполнитель в каталоге
	How    string // alias | text | manual (применяются) · name | surname | org | text-review (проверка)
	Note   string
}

// Set — скомпилированные записи + ручной список, готовые к сопоставлению.
type Set struct {
	Entries  []Entry
	Manual   Manual
	Labels   map[string]string // шаблоны указаний, переопределённые вручную
	Updated  time.Time         // когда обновлены записи реестров
	auto     map[string][]*Entry
	review   map[string][]reviewRef
	titles   map[string][]*Entry // нормализованное название → записи с ним в кавычках
	manual   map[string][]manualRef
	ignore   map[string][]string // ключ исполнителя → нормализованные субъекты («*» — все)
	byName   map[string]*Entry   // нормализованное ФИО/субъект → запись
	songOnly []manualRef         // ручные правила по конкретным песням
}

type reviewRef struct {
	entry *Entry
	how   string
}

type manualRef struct {
	entry *Entry
	rule  ManualRule
}

// Compile готовит Set к сопоставлению.
func Compile(entries []Entry, m Manual, updated time.Time) *Set {
	s := &Set{
		Entries: entries, Manual: m, Labels: m.Labels, Updated: updated,
		auto: map[string][]*Entry{}, review: map[string][]reviewRef{}, titles: map[string][]*Entry{},
		manual: map[string][]manualRef{}, ignore: map[string][]string{}, byName: map[string]*Entry{},
	}
	for i := range s.Entries {
		e := &s.Entries[i]
		s.byName[subjectKey(e.Subject)] = e
		if e.Name != "" {
			s.byName[subjectKey(e.Name)] = e
		}
		for _, a := range e.Aliases {
			k := textnorm.Normalize(a)
			if len([]rune(k)) < 3 {
				continue
			}
			if e.Person {
				s.auto[k] = append(s.auto[k], e)
			} else {
				s.review[k] = append(s.review[k], reviewRef{e, "org"})
			}
		}
		if e.Person {
			if w := strings.Fields(textnorm.Normalize(e.Name)); len(w) >= 2 {
				for _, k := range []string{w[0] + " " + w[1], w[1] + " " + w[0]} {
					s.review[k] = append(s.review[k], reviewRef{e, "name"})
				}
				// В каталоге часто только фамилия или фамилия с инициалом
				// («ПУГАЧЁВА А., ГАЛКИН») — однофамильцев больше, только на проверку.
				if len([]rune(w[0])) >= 4 {
					ini := string([]rune(w[1])[:1])
					for _, k := range []string{w[0], w[0] + " " + ini, ini + " " + w[0]} {
						s.review[k] = append(s.review[k], reviewRef{e, "surname"})
					}
				}
			}
		} else if e.Name != "" && e.Text == "" {
			if k := textnorm.Normalize(e.Name); len([]rune(k)) >= 3 {
				s.review[k] = append(s.review[k], reviewRef{e, "org"})
			}
		}
		for _, t := range e.Titles {
			if k := textnorm.Normalize(t); len([]rune(k)) >= 3 {
				s.titles[k] = append(s.titles[k], e)
			}
		}
	}
	for _, r := range m.Add {
		ref := manualRef{entry: s.byName[subjectKey(r.Subject)], rule: r}
		if ref.entry == nil {
			// Субъекта нет в реестре — самостоятельное правило (например, запрет суда).
			ref.entry = r.standalone()
		}
		for _, a := range r.Artists {
			k := textnorm.Normalize(a)
			s.manual[k] = append(s.manual[k], ref)
		}
		if len(r.Songs) > 0 {
			s.songOnly = append(s.songOnly, ref)
		}
	}
	for _, r := range m.Ignore {
		subj := "*"
		if r.Subject != "" {
			subj = subjectKey(r.Subject)
		}
		for _, a := range r.Artists {
			k := textnorm.Normalize(a)
			s.ignore[k] = append(s.ignore[k], subj)
		}
	}
	return s
}

var (
	quoted = regexp.MustCompile(`[«"“„][^»"”“]*[»"”“]`)
	parens = regexp.MustCompile(`\([^)]*\)`)
)

// subjectKey — ключ для связи ручного списка с реестром: ФИО без псевдонимов и скобок.
func subjectKey(s string) string {
	return textnorm.Normalize(parens.ReplaceAllString(quoted.ReplaceAllString(s, " "), " "))
}

// artistSplit делит «А, Б & В feat. Г» на участников.
var artistSplit = regexp.MustCompile(`(?i)\s*(?:,|&|/|\+|\sи\s|\sx\s|\svs\.?\s|\sfeat\.?\s|\sft\.?\s)\s*`)

// artistKeys — варианты написания исполнителя для сопоставления.
func artistKeys(artist string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if k := textnorm.Normalize(s); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	add(artist) // «ФАМИЛИЯ, ИМЯ» нормализуется в «фамилия имя»
	for _, p := range artistSplit.Split(" "+artist+" ", -1) {
		add(p)
	}
	return out
}

// quotedTitle — «название» в описании записи.
var quotedTitle = regexp.MustCompile(`[«"“„]([^»"”“]{1,120})[»"”“]`)

// QuotedTitles — названия в кавычках из описания записи (для источников).
func QuotedTitles(text string) []string {
	var out []string
	for _, m := range quotedTitle.FindAllStringSubmatch(text, -1) {
		if t := strings.TrimSpace(m[1]); t != "" && !contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// textMatch — исполнитель встречается целым словом в описании записи вне кавычек
// с самим названием (песня «Звезда» в описании ≠ группа «Звезда»).
func textMatch(e *Entry, title string, keys []string) bool {
	rest := " " + textnorm.Normalize(quotedTitle.ReplaceAllStringFunc(e.Text, func(q string) string {
		if m := quotedTitle.FindStringSubmatch(q); m != nil && textnorm.Normalize(m[1]) == title {
			return " "
		}
		return q
	})) + " "
	for _, k := range keys {
		if len([]rune(k)) >= 3 && strings.Contains(rest, " "+k+" ") {
			return true
		}
	}
	return false
}

// MatchSong — действующие ограничения песни и кандидаты на проверку.
func (s *Set) MatchSong(song catalog.Song, now time.Time) (active []Match, review []Match) {
	seen := map[*Entry]bool{}
	keys := artistKeys(song.Artist)
	ignored := func(k string, e *Entry) bool {
		for _, subj := range s.ignore[k] {
			if subj == "*" || subj == subjectKey(e.Subject) || (e.Name != "" && subj == subjectKey(e.Name)) {
				return true
			}
		}
		return false
	}
	ignoredAny := func(e *Entry) bool {
		for _, k := range keys {
			if ignored(k, e) {
				return true
			}
		}
		return false
	}
	for _, k := range keys {
		for _, ref := range s.manual[k] {
			if !seen[ref.entry] && ref.entry.Active(now) && !ignored(k, ref.entry) {
				seen[ref.entry] = true
				active = append(active, Match{Entry: ref.entry, Artist: song.Artist, How: "manual", Note: ref.rule.Note})
			}
		}
		for _, e := range s.auto[k] {
			if !seen[e] && e.Active(now) && !ignored(k, e) {
				seen[e] = true
				active = append(active, Match{Entry: e, Artist: song.Artist, How: "alias"})
			}
		}
		for _, r := range s.review[k] {
			if !seen[r.entry] && r.entry.Active(now) && !ignored(k, r.entry) {
				review = append(review, Match{Entry: r.entry, Artist: song.Artist, How: r.how})
			}
		}
	}
	// Записи, где название песни стоит в кавычках (экстремистские материалы).
	if title := textnorm.Normalize(song.Title); title != "" {
		for _, e := range s.titles[title] {
			if seen[e] || !e.Active(now) || ignoredAny(e) || !textMatch(e, title, keys) {
				continue
			}
			seen[e] = true
			m := Match{Entry: e, Artist: song.Artist, How: "text"}
			if k, _ := LookupKind(e.Kind); k.AutoText {
				active = append(active, m)
			} else {
				m.How = "text-review"
				review = append(review, m)
			}
		}
	}
	for _, ref := range s.songOnly {
		if seen[ref.entry] || !ref.entry.Active(now) {
			continue
		}
		for _, sr := range ref.rule.Songs {
			if textnorm.Normalize(sr.Title) == textnorm.Normalize(song.Title) &&
				(sr.Artist == "" || textnorm.Normalize(sr.Artist) == textnorm.Normalize(song.Artist)) {
				seen[ref.entry] = true
				active = append(active, Match{Entry: ref.entry, Artist: song.Artist, How: "manual", Note: ref.rule.Note})
				break
			}
		}
	}
	return active, review
}

// Label — текст указания для записи по форме вида ограничения.
func (s *Set) Label(e *Entry) string {
	tpl := s.Labels[e.Kind]
	if tpl == "" {
		if k, ok := LookupKind(e.Kind); ok {
			tpl = k.LabelTemplate
		}
	}
	if tpl == "" {
		return e.Subject
	}
	return strings.ReplaceAll(tpl, "{name}", e.Subject)
}

// Stats — итог применения к каталогу.
type Stats struct {
	Regions []string     `json:"regions"`          // юрисдикции заведения
	Kinds   []KindPolicy `json:"kinds"`            // действующие политики по видам
	Labeled int          `json:"labeled"`          // песен с указанием
	Hidden  int          `json:"hidden"`           // скрыто
	Review  int          `json:"review"`           // песен с кандидатами на проверку
	Entries int          `json:"entries"`          // записей реестров
	Updated time.Time    `json:"updated,omitzero"` // когда обновлены реестры
	// LabelScale — размер шрифта указания относительно основного текста
	// (по постановлению — вдвое крупнее); интерфейс берёт его отсюда.
	LabelScale float64 `json:"labelScale"`
}

// Apply применяет политики к песням: label — добавляет указания, hide — убирает.
// Если у песни несколько ограничений, действует самое строгое.
func (s *Set) Apply(songs []catalog.Song, cfg Config, now time.Time) ([]catalog.Song, Stats) {
	st := Stats{Regions: cfg.Regions, Kinds: cfg.Policies()}
	if s == nil || !cfg.Enabled() {
		return songs, st
	}
	st.Entries, st.Updated = len(s.Entries), s.Updated
	out := make([]catalog.Song, 0, len(songs))
	for _, song := range songs {
		active, review := s.MatchSong(song, now)
		if len(review) > 0 {
			st.Review++
		}
		policy := PolicyOff
		var labels []catalog.Restriction
		for _, m := range active {
			p := cfg.Effective(m.Entry.Kind)
			if p == PolicyOff {
				continue
			}
			if p.Stricter(policy) {
				policy = p
			}
			labels = append(labels, catalog.Restriction{
				Kind: m.Entry.Kind, Subject: m.Entry.Subject, Label: s.Label(m.Entry), Note: m.Note,
			})
		}
		switch policy {
		case PolicyHide:
			st.Hidden++
			continue
		case PolicyLabel:
			song.Restrictions = labels
			st.Labeled++
		}
		out = append(out, song)
	}
	return out, st
}

// Report — отчёт по каталогу: что применяется и что требует проверки.
type Report struct {
	Active []ReportLine
	Review []ReportLine
}

// ReportLine — исполнитель каталога и запись реестра.
type ReportLine struct {
	Artist  string
	Title   string // для совпадений по тексту записи
	Kind    string
	Subject string
	How     string
	Songs   int
	Note    string
}

// Report строит отчёт для ручной проверки (утилиты cmd/restrictions-*).
func (s *Set) Report(songs []catalog.Song, now time.Time) Report {
	type key struct{ artist, title, subject, how string }
	act, rev := map[key]*ReportLine{}, map[key]*ReportLine{}
	add := func(dst map[key]*ReportLine, song catalog.Song, m Match) {
		title := ""
		if strings.HasPrefix(m.How, "text") {
			title = song.Title
		}
		k := key{m.Artist, title, m.Entry.Subject, m.How}
		if dst[k] == nil {
			dst[k] = &ReportLine{Artist: m.Artist, Title: title, Kind: m.Entry.Kind, Subject: m.Entry.Subject, How: m.How, Note: m.Note}
		}
		dst[k].Songs++
	}
	for _, song := range songs {
		active, review := s.MatchSong(song, now)
		for _, m := range active {
			add(act, song, m)
		}
		for _, m := range review {
			add(rev, song, m)
		}
	}
	flat := func(m map[key]*ReportLine) []ReportLine {
		out := make([]ReportLine, 0, len(m))
		for _, l := range m {
			out = append(out, *l)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Songs != out[j].Songs {
				return out[i].Songs > out[j].Songs
			}
			return out[i].Artist+out[i].Title < out[j].Artist+out[j].Title
		})
		return out
	}
	return Report{Active: flat(act), Review: flat(rev)}
}
