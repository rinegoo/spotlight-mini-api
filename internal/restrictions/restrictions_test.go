package restrictions_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/ru"
)

func day(s string) *time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return &t
}

var ruConfig = restrictions.Config{Regions: []string{"RU"}}

// Вымышленные записи в формате реестров.
func entries() []restrictions.Entry {
	return []restrictions.Entry{
		{Kind: ru.KindInoagent, Subject: `Тестов Тест Тестович "Пит Сид"`, Name: "Тестов Тест Тестович", Person: true, Aliases: []string{"Пит Сид"}, Since: day("2023-01-01")},
		{Kind: ru.KindInoagent, Subject: "Примеров Пример Примерович", Name: "Примеров Пример Примерович", Person: true, Since: day("2023-01-01")},
		{Kind: ru.KindInoagent, Subject: "Лунная Анна Сергеевна", Name: "Лунная Анна Сергеевна", Person: true},
		{Kind: ru.KindInoagent, Subject: `Бывший Олег Олегович "Экс"`, Name: "Бывший Олег Олегович", Person: true, Aliases: []string{"Экс"}, Since: day("2022-01-01"), Until: day("2024-01-01")},
		{Kind: ru.KindInoagent, Subject: "Интернет-ресурс «Рассвет»", Name: "Интернет-ресурс", Aliases: []string{"Рассвет"}},
		// Экстремистские материалы: название в кавычках + исполнитель в описании.
		{Kind: ru.KindExtremist, Subject: "список, № 10", Text: `Текст песни исполнителя Выдумкин (Vydumkin) «Запретная песня» (решение суда)`, Titles: []string{"Запретная песня"}},
		{Kind: ru.KindExtremist, Subject: "список, № 11", Text: `Песни группы «Чужие»: «Белая стена», «Волк»`, Titles: []string{"Чужие", "Белая стена", "Волк"}},
	}
}

func song(id, artist, title string) catalog.Song {
	return catalog.Song{ID: id, Number: id, Artist: artist, Title: title}
}

func TestMatchSong(t *testing.T) {
	manual := restrictions.Manual{
		Add: []restrictions.ManualRule{
			{Subject: "Лунная Анна Сергеевна", Artists: []string{"ЛУНА"}},                                // сценическое имя
			{Subject: "Тестов Тест Тестович", Artists: []string{"ГРУППА ПИТА"}, Note: "участник группы"}, // группа
			{Subject: "Примеров Пример Примерович", Artists: []string{"ПРИМЕРОВ ПРИМЕР"}},                // подтверждение ФИО
			{Kind: ru.KindCourtBan, Subject: "Решение суда № 1", Songs: []restrictions.SongRef{{Title: "ЗАПРЕТНАЯ ТОЖЕ"}}},
		},
		Ignore: []restrictions.ManualIgnore{{Subject: "Интернет-ресурс «Рассвет»", Artists: []string{"РАССВЕТ"}}},
	}
	set := restrictions.Compile(entries(), manual, time.Now())
	now := time.Now()
	cases := []struct {
		song     catalog.Song
		active   []string // How
		inReview bool
	}{
		{song("1", "ПИТ СИД", "А"), []string{"alias"}, false},                // псевдоним — автоматически
		{song("2", "КТО-ТО & ПИТ СИД", "Б"), []string{"alias"}, false},       // совместная песня
		{song("3", "ТЕСТОВ ТЕСТ", "В"), nil, true},                           // только ФИО — на проверку
		{song("4", "ПРИМЕРОВ, ПРИМЕР", "Г"), []string{"manual"}, false},      // «ФАМИЛИЯ, ИМЯ», подтверждено
		{song("5", "ЛУНА", "Д"), []string{"manual"}, false},                  // добавлено вручную
		{song("6", "ГРУППА ПИТА", "Е"), []string{"manual"}, false},           // группа
		{song("7", "ЭКС", "Ж"), nil, false},                                  // исключён из реестра — снято
		{song("8", "РАССВЕТ", "З"), nil, false},                              // организация, отклонено
		{song("9", "НЕКТО", "ЗАПРЕТНАЯ ТОЖЕ"), []string{"manual"}, false},    // ручное правило по песне
		{song("10", "VYDUMKIN", "ЗАПРЕТНАЯ ПЕСНЯ"), []string{"text"}, false}, // экстремистский: исполнитель в описании
		{song("11", "ДРУГОЙ", "ЗАПРЕТНАЯ ПЕСНЯ"), nil, false},                // то же название, другой исполнитель
		{song("12", "ЧУЖИЕ", "БЕЛАЯ СТЕНА"), []string{"text"}, false},        // группа в кавычках — тоже исполнитель
		{song("13", "БЕЛАЯ СТЕНА", "БЕЛАЯ СТЕНА"), nil, false},               // совпало только название-в-кавычках
		{song("14", "СЛУЧАЙНЫЙ ИСПОЛНИТЕЛЬ", "И"), nil, false},               // никого
		{song("15", "КТО-ТО А., ТЕСТОВ", "К"), nil, true},                    // только фамилия в дуэте — на проверку
		{song("16", "ТЕСТОВ Т.", "Л"), nil, true},                            // фамилия с инициалом — на проверку
		{song("17", "ТЕСТОВ О.", "М"), nil, false},                           // другой инициал — не он
	}
	for _, c := range cases {
		active, review := set.MatchSong(c.song, now)
		var got []string
		for _, m := range active {
			got = append(got, m.How)
		}
		if strings.Join(got, ",") != strings.Join(c.active, ",") || (len(review) > 0) != c.inReview {
			t.Errorf("%q — %q: active=%v review=%d, want %v review=%v", c.song.Artist, c.song.Title, got, len(review), c.active, c.inReview)
		}
	}
	// До даты исключения запись действовала.
	if active, _ := set.MatchSong(song("7", "ЭКС", "Ж"), *day("2023-06-01")); len(active) != 1 {
		t.Error("excluded entry must apply before its exclusion date")
	}
}

func TestConfigMinimums(t *testing.T) {
	cfg := restrictions.Config{Regions: []string{"RU"}, Overrides: map[string]restrictions.Policy{
		ru.KindInoagent:  restrictions.PolicyHide,  // ужесточить — можно
		ru.KindExtremist: restrictions.PolicyLabel, // ослабить ниже минимума — нельзя
	}}
	if p := cfg.Effective(ru.KindInoagent); p != restrictions.PolicyHide {
		t.Errorf("inoagent = %s, want hide", p)
	}
	if p := cfg.Effective(ru.KindExtremist); p != restrictions.PolicyHide {
		t.Errorf("extremist = %s, want hide (minimum)", p)
	}
	if p := ruConfig.Effective(ru.KindInoagent); p != restrictions.PolicyLabel {
		t.Errorf("inoagent default = %s, want label", p)
	}
	for _, kp := range cfg.Policies() {
		if kp.Kind == ru.KindExtremist && kp.Requested != restrictions.PolicyLabel {
			t.Errorf("requested below minimum must be reported: %+v", kp)
		}
	}
	// Вне региона RU российские виды не применяются.
	other := restrictions.Config{Regions: []string{"BY"}}
	if p := other.Effective(ru.KindExtremist); p != restrictions.PolicyOff {
		t.Errorf("RU kind in BY = %s, want off", p)
	}
	if (restrictions.Config{Regions: restrictions.ParseRegions("none")}).Enabled() {
		t.Error("region none must disable restrictions")
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{"RESTRICTIONS_POLICY_RU_INOAGENT": "hide"}
	cfg, err := restrictions.ConfigFromEnv("ru", func(k string) string { return env[k] })
	if err != nil || cfg.Effective(ru.KindInoagent) != restrictions.PolicyHide || cfg.Regions[0] != "RU" {
		t.Errorf("cfg = %+v, %v", cfg, err)
	}
	env["RESTRICTIONS_POLICY_RU_EXTREMIST"] = "block"
	if _, err := restrictions.ConfigFromEnv("RU", func(k string) string { return env[k] }); err == nil {
		t.Error("invalid policy accepted")
	}
}

func TestApply(t *testing.T) {
	set := restrictions.Compile(entries(), restrictions.Manual{}, time.Now())
	songs := []catalog.Song{
		song("1", "ПИТ СИД", "А"),
		song("2", "ДРУГОЙ", "Б"),
		song("3", "VYDUMKIN", "ЗАПРЕТНАЯ ПЕСНЯ"),
		song("4", "ПИТ СИД & VYDUMKIN", "ЗАПРЕТНАЯ ПЕСНЯ"), // оба вида — действует самое строгое
	}
	out, st := set.Apply(songs, ruConfig, time.Now())
	if len(out) != 2 || out[0].ID != "1" || out[1].ID != "2" || st.Labeled != 1 || st.Hidden != 2 {
		t.Fatalf("apply: %+v %+v", out, st)
	}
	r := out[0].Restrictions[0]
	if r.Kind != ru.KindInoagent || !strings.Contains(r.Label, `ИНОСТРАННЫМ АГЕНТОМ Тестов Тест Тестович "Пит Сид"`) ||
		!strings.HasPrefix(r.Label, "НАСТОЯЩИЙ МАТЕРИАЛ (ИНФОРМАЦИЯ)") {
		t.Errorf("label = %+v", r)
	}
	if len(songs[0].Restrictions) != 0 {
		t.Error("Apply must not modify input songs")
	}
	if out, _ := set.Apply(songs, restrictions.Config{}, time.Now()); len(out) != 4 {
		t.Error("no region — nothing applied")
	}
	var nilSet *restrictions.Set
	if out, _ := nilSet.Apply(songs, ruConfig, time.Now()); len(out) != 4 {
		t.Error("nil set must pass songs through")
	}
}

func TestParsePolicy(t *testing.T) {
	for in, want := range map[string]restrictions.Policy{"LABEL": "label", "hide": "hide", "off": "off"} {
		if got, ok := restrictions.ParsePolicy(in); !ok || got != want {
			t.Errorf("ParsePolicy(%q) = %q", in, got)
		}
	}
	if _, ok := restrictions.ParsePolicy("block"); ok {
		t.Error("unknown policy accepted")
	}
}

func TestLoadManual(t *testing.T) {
	dir := t.TempDir()
	if m, err := restrictions.LoadManual(filepath.Join(dir, "none.yaml")); err != nil || len(m.Add) != 0 {
		t.Errorf("missing file: %v %v", m, err)
	}
	p := filepath.Join(dir, "manual.yaml")
	os.WriteFile(p, []byte("add:\n  - subject: Лунная Анна\n    artists: [ЛУНА]\nignore:\n  - artists: [X]\n"), 0o644)
	m, err := restrictions.LoadManual(p)
	if err != nil || len(m.Add) != 1 || m.Add[0].Artists[0] != "ЛУНА" || len(m.Ignore) != 1 {
		t.Errorf("manual = %+v, %v", m, err)
	}
	os.WriteFile(p, []byte("add:\n  - subject: Без исполнителей\n"), 0o644)
	if _, err := restrictions.LoadManual(p); err == nil {
		t.Error("rule without artists accepted")
	}
}

type stubProvider struct {
	id      string
	entries []restrictions.Entry
}

func (p *stubProvider) ID() string { return p.id }

func (p *stubProvider) Fetch(context.Context) ([]restrictions.Entry, time.Time, error) {
	return p.entries, time.Now(), nil
}

func TestManagerSyncAndApply(t *testing.T) {
	dir := t.TempDir()
	prov := &stubProvider{id: "stub", entries: entries()}
	m := restrictions.NewManager(dir, ruConfig, prov)
	changed := 0
	m.OnChange(func() { changed++ })
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	if got := m.Apply([]catalog.Song{song("1", "ПИТ СИД", "А")}); len(got[0].Restrictions) != 0 {
		t.Error("no data yet — nothing must be applied")
	}
	if !m.NeedsSync(prov, time.Hour) {
		t.Error("NeedsSync without data must be true")
	}
	if err := m.Sync(t.Context()); err != nil || changed != 1 {
		t.Fatalf("sync: %v changed=%d", err, changed)
	}
	if m.NeedsSync(prov, time.Hour) {
		t.Error("fresh data must not need sync")
	}
	if got := m.Apply([]catalog.Song{song("1", "ПИТ СИД", "А")}); len(got[0].Restrictions) != 1 {
		t.Error("synced entries not applied")
	}
	if st := m.Stats(); st.Labeled != 1 || st.Entries != len(entries()) || st.Regions[0] != "RU" {
		t.Errorf("stats = %+v", st)
	}
}

// Сервер применяет файлы любых источников — и тех, которые сам не синхронизирует.
func TestManagerLoadsForeignProviderFiles(t *testing.T) {
	dir := t.TempDir()
	if err := restrictions.SyncProvider(t.Context(), dir, &stubProvider{id: "other-source", entries: entries()[:1]}); err != nil {
		t.Fatal(err)
	}
	m := restrictions.NewManager(dir, ruConfig) // без своих источников
	if err := m.Load(); err != nil || len(m.Current().Entries) != 1 {
		t.Fatalf("load: %v entries=%d", err, len(m.Current().Entries))
	}
	// Битый файл одного источника не мешает остальным.
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644)
	if err := m.Load(); err == nil || len(m.Current().Entries) != 1 {
		t.Errorf("broken file: err=%v entries=%d", err, len(m.Current().Entries))
	}
}

// Изменения файлов извне (утилита, правка manual.yaml) подхватываются без перезапуска.
func TestManagerWatch(t *testing.T) {
	old := restrictions.WatchInterval
	restrictions.WatchInterval = 20 * time.Millisecond
	defer func() { restrictions.WatchInterval = old }()

	dir := t.TempDir()
	m := restrictions.NewManager(dir, ruConfig)
	m.Load()
	changed := make(chan struct{}, 10)
	m.OnChange(func() { changed <- struct{}{} })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.Run(ctx, 0)

	os.WriteFile(filepath.Join(dir, restrictions.ManualFile), []byte("add:\n  - subject: Кто-то\n    artists: [ЛУНА]\n"), 0o644)
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("manual.yaml change not picked up")
	}
	if got := m.Apply([]catalog.Song{song("1", "ЛУНА", "А")}); len(got[0].Restrictions) != 1 {
		t.Error("manual change not applied")
	}
	restrictions.SyncProvider(t.Context(), dir, &stubProvider{id: "ext", entries: entries()})
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("external provider file not picked up")
	}
	if len(m.Current().Entries) != len(entries()) {
		t.Errorf("entries = %d", len(m.Current().Entries))
	}
}

// Одновременная запись одного источника не портит файл.
func TestConcurrentSync(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			restrictions.SyncProvider(context.Background(), dir, &stubProvider{id: "same", entries: entries()[:1+i%3]})
		}()
	}
	wg.Wait()
	var f restrictions.ProviderFile
	b, err := os.ReadFile(filepath.Join(dir, "same.json"))
	if err != nil || json.Unmarshal(b, &f) != nil || len(f.Entries) == 0 {
		t.Fatalf("file corrupted: %v", err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(tmp) != 0 {
		t.Errorf("temp files left: %v", tmp)
	}
}
