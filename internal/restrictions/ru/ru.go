// Package ru — ограничения контента в Российской Федерации: виды и формы
// указаний. Источники данных — подпакеты (inoagents — реестр иноагентов Минюста).
package ru

import "github.com/rinegoo/spotlight-mini-api/internal/restrictions"

// Виды ограничений РФ.
const (
	KindInoagent  = "ru.inoagent"  // лицо в реестре иностранных агентов Минюста (255-ФЗ)
	KindExtremist = "ru.extremist" // федеральный список экстремистских материалов (114-ФЗ)
	KindCourtBan  = "ru.court_ban" // запрет решением суда (например, пропаганда наркотиков)
	Jurisdiction  = "RU"
)

// InoagentLabel — форма указания на статус иностранного агента: постановление
// Правительства РФ от 22.11.2022 № 2108 (в ред. от 16.08.2025 № 1232), ст. 9 255-ФЗ.
// Формулировку нельзя сокращать и перефразировать; оформление — шрифт вдвое
// крупнее основного текста, контрастный цвет, под заголовком материала.
// Действующую редакцию подтвердить у юриста; при необходимости переопределить
// в manual.yaml (labels.ru.inoagent) без изменения кода.
const InoagentLabel = "НАСТОЯЩИЙ МАТЕРИАЛ (ИНФОРМАЦИЯ) ПРОИЗВЕДЕН, РАСПРОСТРАНЕН И (ИЛИ) НАПРАВЛЕН " +
	"ИНОСТРАННЫМ АГЕНТОМ {name} ЛИБО КАСАЕТСЯ ДЕЯТЕЛЬНОСТИ ИНОСТРАННОГО АГЕНТА {name}"

// Обязательные минимумы (не юридическая консультация, см. docs/content-restrictions):
//   - иноагенты — указание по установленной форме (ст. 9 255-ФЗ, ч. 2.1 ст. 13.15 КоАП);
//     скрыть — по решению заведения;
//   - экстремистские материалы — распространение запрещено (ст. 13 114-ФЗ,
//     ст. 20.29 КоАП: юрлицам до 1 млн ₽ или приостановление деятельности) — только скрывать;
//   - запрет решением суда (например, пропаганда наркотиков) — только скрывать.
func init() {
	restrictions.RegisterKind(restrictions.Kind{
		ID: KindInoagent, Jurisdiction: Jurisdiction, Title: "Иностранный агент", LabelTemplate: InoagentLabel,
		Default: restrictions.PolicyLabel, Min: restrictions.PolicyLabel,
	})
	restrictions.RegisterKind(restrictions.Kind{
		ID: KindExtremist, Jurisdiction: Jurisdiction, Title: "Экстремистский материал",
		LabelTemplate: "Материал включён в федеральный список экстремистских материалов: {name}",
		Default:       restrictions.PolicyHide, Min: restrictions.PolicyHide,
		// Пропуск (штраф до 1 млн ₽) опаснее лишнего скрытия одной песни: совпадения
		// «название в кавычках + исполнитель» применяются сразу; ложные — в ignore.
		AutoText: true,
	})
	restrictions.RegisterKind(restrictions.Kind{
		ID: KindCourtBan, Jurisdiction: Jurisdiction, Title: "Запрещено решением суда",
		LabelTemplate: "Распространение запрещено: {name}",
		Default:       restrictions.PolicyHide, Min: restrictions.PolicyHide,
	})
}
