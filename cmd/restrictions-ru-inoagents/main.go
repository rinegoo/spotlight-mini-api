// Команда restrictions-ru-inoagents — ограничения РФ: реестр иностранных агентов Минюста.
//
// Синхронизирует реестр в каталог данных и показывает, как ограничения ложатся на
// каталог: что применяется и какие совпадения надо проверить (однофамильцы,
// организации) и внести в manual.yaml. Другие источники — отдельные команды
// restrictions-<юрисдикция>-<источник>.
//
//	restrictions-ru-inoagents -data ./data -sync                       # скачать реестр
//	restrictions-ru-inoagents -data ./data -file export.xlsx           # взять выгрузку из файла
//	restrictions-ru-inoagents -data ./data -catalog examples/base.db   # отчёт по каталогу
package main

import (
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/cli"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/ru/inoagents"
)

func main() {
	cli.Run("restrictions-ru-inoagents", cli.Source{
		Provider:  inoagents.Provider{},
		ParseFile: func(b []byte) ([]restrictions.Entry, time.Time, error) { return inoagents.Parse(b) },
		FileHint:  "выгрузка XLSX реестра иноагентов",
	})
}
