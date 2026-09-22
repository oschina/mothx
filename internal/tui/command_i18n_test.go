package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestCommandSpecsHaveStableSyntaxAndTranslations(t *testing.T) {
	seen := make(map[string]bool, len(commandSpecs))
	for _, spec := range commandSpecs {
		if spec.Name == "" || !strings.HasPrefix(spec.Name, "/") {
			t.Fatalf("invalid command name: %#v", spec)
		}
		if seen[spec.Name] {
			t.Fatalf("duplicate command spec: %s", spec.Name)
		}
		seen[spec.Name] = true
		if spec.Usage == "" || !strings.HasPrefix(spec.Usage, spec.Name) {
			t.Fatalf("invalid usage for %s: %q", spec.Name, spec.Usage)
		}
		for _, lang := range []i18n.Language{i18n.LanguageEN, i18n.LanguageZH} {
			text := i18n.New(lang).Text(spec.Description)
			if text == "" || text == string(spec.Description) {
				t.Fatalf("missing %s description for %s (%s)", lang, spec.Name, spec.Description)
			}
		}
	}
}

func TestCommandHelpLocalizesDescriptionsButPreservesSyntax(t *testing.T) {
	en := commandHelpText(i18n.New(i18n.LanguageEN))
	zh := commandHelpText(i18n.New(i18n.LanguageZH))
	if en == zh {
		t.Fatal("English and Chinese command help should differ")
	}
	for _, spec := range commandSpecs {
		if !strings.Contains(en, spec.Usage) || !strings.Contains(zh, spec.Usage) {
			t.Fatalf("command syntax %q must be preserved in both help outputs", spec.Usage)
		}
		if !strings.Contains(en, i18n.New(i18n.LanguageEN).Text(spec.Description)) {
			t.Fatalf("English help missing description for %s", spec.Name)
		}
		if !strings.Contains(zh, i18n.New(i18n.LanguageZH).Text(spec.Description)) {
			t.Fatalf("Chinese help missing description for %s", spec.Name)
		}
	}
}

func TestCommandSuggestionsUseCommandSpecs(t *testing.T) {
	for _, lang := range []i18n.Language{i18n.LanguageEN, i18n.LanguageZH} {
		items := commandSuggestionItems(i18n.New(lang))
		if len(items) != len(commandSpecs) {
			t.Fatalf("%s suggestions=%d, specs=%d", lang, len(items), len(commandSpecs))
		}
		for index, spec := range commandSpecs {
			item := items[index]
			if item.Label != spec.Name || item.Value != spec.Value {
				t.Fatalf("%s item %d = %#v, want name=%q value=%q", lang, index, item, spec.Name, spec.Value)
			}
			if item.Description != i18n.New(lang).Text(spec.Description) {
				t.Fatalf("%s description for %s = %q", lang, spec.Name, item.Description)
			}
		}
	}
}

func TestCommandUsageLocalizesPrefixOnly(t *testing.T) {
	const syntax = "/sessions set <id>"
	if got := commandUsage(i18n.New(i18n.LanguageEN), syntax); got != "Usage: "+syntax {
		t.Fatalf("English usage = %q", got)
	}
	if got := commandUsage(i18n.New(i18n.LanguageZH), syntax); got != "用法："+syntax {
		t.Fatalf("Chinese usage = %q", got)
	}
}
