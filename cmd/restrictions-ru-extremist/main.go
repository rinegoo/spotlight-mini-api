// Команда restrictions-ru-extremist — ограничения РФ: федеральный список
// экстремистских материалов Минюста (музыкальные записи).
//
//	restrictions-ru-extremist -data ./data -sync                       # скачать список
//	restrictions-ru-extremist -data ./data -file exportfsm.csv         # взять выгрузку из файла
//	restrictions-ru-extremist -data ./data -catalog examples/base.db   # отчёт по каталогу
package main

import (
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/cli"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/ru/extremist"
)

func main() {
	cli.Run("restrictions-ru-extremist", cli.Source{
		Provider: extremist.Provider{},
		ParseFile: func(b []byte) ([]restrictions.Entry, time.Time, error) {
			entries, err := extremist.Parse(b)
			return entries, time.Time{}, err
		},
		FileHint: "выгрузка CSV списка экстремистских материалов",
	})
}
