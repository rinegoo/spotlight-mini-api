//go:build race

package search

// Детектор гонок замедляет код в разы — проверки времени отключаются.
const raceEnabled = true
