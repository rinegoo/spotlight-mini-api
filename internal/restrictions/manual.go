package restrictions

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Manual — ручная часть списка ограничений (manual.yaml в каталоге данных).
//
//	labels:                 # переопределить форму указания для вида
//	  ru.inoagent: "…{name}…"
//	add:                    # дополнения: исполнители, которых нет в реестре под этим именем
//	  - subject: Фамилия Имя Отчество      # как в реестре: даты берутся оттуда
//	    artists: [СЦЕНИЧЕСКОЕ ИМЯ]
//	  - subject: Фамилия Имя Отчество участника
//	    artists: [НАЗВАНИЕ ГРУППЫ]
//	    note: участник группы
//	  - kind: ru.court_ban                          # правило без записи в реестре
//	    subject: Решение суда № …
//	    since: 2025-06-01
//	    songs: [{artist: …, title: …}]
//	ignore:                 # ложные совпадения (однофамильцы, организации)
//	  - subject: Название организации из реестра
//	    artists: [ОДНОИМЁННЫЙ ИСПОЛНИТЕЛЬ]
type Manual struct {
	Labels map[string]string `yaml:"labels"`
	Add    []ManualRule      `yaml:"add"`
	Ignore []ManualIgnore    `yaml:"ignore"`
}

// ManualRule — дополнение: субъект (обычно из реестра) и его написания в каталоге.
type ManualRule struct {
	Kind    string    `yaml:"kind"`    // для правил без записи в реестре; по умолчанию ru.inoagent
	Subject string    `yaml:"subject"` // ФИО / наименование как в реестре (псевдонимы и скобки можно опустить)
	Artists []string  `yaml:"artists"`
	Songs   []SongRef `yaml:"songs"`
	Note    string    `yaml:"note"`
	Since   string    `yaml:"since"` // ГГГГ-ММ-ДД — только для правил без записи в реестре
	Until   string    `yaml:"until"`
	Source  string    `yaml:"source"`
}

// SongRef — конкретная песня (для экстремистских материалов и запретов судов).
type SongRef struct {
	Artist string `yaml:"artist"`
	Title  string `yaml:"title"`
}

// ManualIgnore — не применять совпадения субъекта (или любые, если subject пуст) к исполнителям.
type ManualIgnore struct {
	Subject string   `yaml:"subject"`
	Artists []string `yaml:"artists"`
	Note    string   `yaml:"note"`
}

// DefaultKind — вид для ручных правил без указания kind.
const DefaultKind = "ru.inoagent"

func (r ManualRule) standalone() *Entry {
	kind := r.Kind
	if kind == "" {
		kind = DefaultKind
	}
	e := &Entry{Kind: kind, Subject: r.Subject, Name: r.Subject, Source: r.Source}
	if t, err := time.Parse(time.DateOnly, r.Since); err == nil {
		e.Since = &t
	}
	if t, err := time.Parse(time.DateOnly, r.Until); err == nil {
		e.Until = &t
	}
	return e
}

// LoadManual читает manual.yaml; нет файла — пустой список.
func LoadManual(path string) (Manual, error) {
	var m Manual
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	for i, r := range m.Add {
		if r.Subject == "" {
			return m, fmt.Errorf("%s: add[%d]: subject is required", path, i)
		}
		if len(r.Artists) == 0 && len(r.Songs) == 0 {
			return m, fmt.Errorf("%s: add[%d] (%s): artists or songs required", path, i, r.Subject)
		}
	}
	for i, r := range m.Ignore {
		if len(r.Artists) == 0 {
			return m, fmt.Errorf("%s: ignore[%d]: artists required", path, i)
		}
	}
	return m, nil
}
