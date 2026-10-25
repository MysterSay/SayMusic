package player

import (
	"encoding/json"
	"strings"
	"sync"
)

var localeOnce sync.Once
var englishMessages map[string]string

func localized(language, text string) string {
	if language != "en" {
		return text
	}
	localeOnce.Do(func() { b, _ := assets.ReadFile("web/locales-en.json"); _ = json.Unmarshal(b, &englishMessages) })
	if translated, ok := englishMessages[text]; ok {
		return translated
	}
	return text
}
func (a *App) uiText(text string) string { return localized(a.config().Language, text) }
func normalizeLanguage(value string) string {
	if strings.EqualFold(value, "en") {
		return "en"
	}
	return "uk"
}
