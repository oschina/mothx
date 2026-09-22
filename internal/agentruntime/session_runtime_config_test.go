package agentruntime

import (
	"errors"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestSessionRuntimeExpertConfigOptionUsesRuntimeBindingRules(t *testing.T) {
	workDir := t.TempDir()
	writeExpertFixtures(t, workDir)
	manager := session.New(workDir, t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	model := &provider.Model{ID: "model", Name: "Model"}
	p := provider.NewMockProvider("test-provider", []*provider.Model{model}, nil)
	runtime := &SessionRuntime{
		ID: manager.GetHeader().ID, Source: SourceACP, EntrySource: SourceACP,
		WorkDir: workDir, Manager: manager,
	}
	if err := runtime.ConfigureSession(p, "test-provider", model, ModeYolo, provider.ThinkingMedium); err != nil {
		t.Fatal(err)
	}

	options := runtime.ConfigOptions()
	expertOption := SessionConfigOption{}
	for _, option := range options {
		if option.ID == ConfigOptionExpert {
			expertOption = option
			break
		}
	}
	if expertOption.ID == "" || expertOption.CurrentValue != "" {
		t.Fatalf("initial expert option = %#v", expertOption)
	}
	if len(expertOption.Options) < 3 || expertOption.Options[0].Value != "" {
		t.Fatalf("expert choices = %#v, want unbound/studio/solo", expertOption.Options)
	}

	if err := runtime.SetConfigOption(ConfigOptionExpert, "studio"); err != nil {
		t.Fatalf("bind expert through config option: %v", err)
	}
	if got := manager.GetExpertID(); got != "studio" || !runtime.TeamExpertActive() {
		t.Fatalf("expert binding = %q, team=%v", got, runtime.TeamExpertActive())
	}
	if got := optionCurrentValue(runtime.ConfigOptions(), ConfigOptionExpert); got != "studio" {
		t.Fatalf("bound expert option = %q", got)
	}
	if err := runtime.SetConfigOption(ConfigOptionExpert, "solo"); !errors.Is(err, ErrExpertSwitchRequiresFork) {
		t.Fatalf("in-place expert switch error = %v, want ErrExpertSwitchRequiresFork", err)
	}
	if err := runtime.SetConfigOption(ConfigOptionExpert, ""); err != nil {
		t.Fatalf("unbind expert through config option: %v", err)
	}
	if got := manager.GetExpertID(); got != "" || runtime.TeamExpertActive() {
		t.Fatalf("expert unbind = %q, team=%v", got, runtime.TeamExpertActive())
	}
}

func TestSessionRuntimeConfigOptionsPersistAndRejectInvalidModel(t *testing.T) {
	workDir := t.TempDir()
	manager := session.New(workDir, t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	modelOne := &provider.Model{ID: "model-one", Name: "Model One", ContextWindow: 32768, Reasoning: true}
	modelTwo := &provider.Model{ID: "model-two", Name: "Model Two", ContextWindow: 65536, Reasoning: true}
	p := provider.NewMockProvider("test-provider", []*provider.Model{modelOne, modelTwo}, nil)
	runtime := &SessionRuntime{
		ID: manager.GetHeader().ID, Source: SourceACP, EntrySource: SourceACP,
		WorkDir: workDir, Manager: manager,
	}
	if err := runtime.ConfigureSession(p, "test-provider", modelOne, ModeAgent, provider.ThinkingMedium); err != nil {
		t.Fatal(err)
	}
	options := runtime.ConfigOptions()
	if got := optionCurrentValue(options, ConfigOptionModel); got != "test-provider/model-one" {
		t.Fatalf("initial model option = %q", got)
	}
	if err := runtime.SetConfigOption(ConfigOptionModel, "test-provider/model-two"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := runtime.SetConfigOption(ConfigOptionMode, ModePlan); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if err := runtime.SetConfigOption(ConfigOptionThinkingLevel, string(provider.ThinkingHigh)); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}
	_, _, currentModel, currentMode, currentThinking := runtime.ConfigSnapshot()
	if currentModel == nil || currentModel.ID != modelTwo.ID || currentMode != ModePlan || currentThinking != provider.ThinkingHigh {
		t.Fatalf("runtime config = model %v, mode %q, thinking %q", currentModel, currentMode, currentThinking)
	}
	if entry, ok := manager.GetLatestModelChange(); !ok || entry.ModelID != modelTwo.ID {
		t.Fatalf("persisted model entry = %#v, ok=%v", entry, ok)
	}
	if entry, ok := manager.GetLatestModeChange(); !ok || entry.Mode != ModePlan {
		t.Fatalf("persisted mode entry = %#v, ok=%v", entry, ok)
	}
	if entry, ok := manager.GetLatestThinkingLevelChange(); !ok || entry.ThinkingLevel != string(provider.ThinkingHigh) {
		t.Fatalf("persisted thinking entry = %#v, ok=%v", entry, ok)
	}
	if err := runtime.SetConfigOption(ConfigOptionModel, "test-provider/missing"); err == nil {
		t.Fatal("invalid model unexpectedly succeeded")
	}
	_, _, currentModel, _, _ = runtime.ConfigSnapshot()
	if currentModel == nil || currentModel.ID != modelTwo.ID {
		t.Fatalf("invalid model changed runtime model to %#v", currentModel)
	}
}

func TestSessionConfigOptionsOmitThinkingForNonReasoningModel(t *testing.T) {
	model := &provider.Model{ID: "plain-model", Name: "Plain Model"}
	options := SessionConfigOptions("test-provider", []*provider.Model{model}, model, ModeAgent, provider.ThinkingMedium)
	if optionCurrentValue(options, ConfigOptionThinkingLevel) != "" {
		t.Fatal("non-reasoning model advertised a thinking-level option")
	}
	for _, option := range options {
		if option.ID == ConfigOptionThinkingLevel {
			t.Fatalf("non-reasoning model advertised thinking option: %#v", option)
		}
	}
}

func TestSessionRuntimeProviderSwitchCascadesModels(t *testing.T) {
	workDir := t.TempDir()
	manager := session.New(workDir, t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	moarkModel := &provider.Model{ID: "moark-model", Name: "Moark Model", Reasoning: true}
	volcModel := &provider.Model{ID: "ark-code-latest", Name: "Ark Code Latest"}
	moark := provider.NewMockProvider("moark", []*provider.Model{moarkModel}, nil)
	volc := provider.NewMockProvider("volcengine-agentplan", []*provider.Model{volcModel}, nil)
	runtime := &SessionRuntime{
		ID: manager.GetHeader().ID, Source: SourceACP, EntrySource: SourceACP,
		WorkDir: workDir, Manager: manager, Providers: ProviderCatalog{"moark": moark, "volcengine-agentplan": volc},
	}
	if err := runtime.ConfigureSession(moark, "moark", moarkModel, ModeYolo, provider.ThinkingMedium); err != nil {
		t.Fatal(err)
	}
	options := runtime.ConfigOptions()
	if got := optionCurrentValue(options, ConfigOptionProvider); got != "moark" {
		t.Fatalf("provider option = %q", got)
	}
	if got := optionCurrentValue(options, ConfigOptionModel); got != "moark/moark-model" {
		t.Fatalf("initial model option = %q", got)
	}
	if err := runtime.SetConfigOption(ConfigOptionProvider, "volcengine-agentplan"); err != nil {
		t.Fatalf("switch provider: %v", err)
	}
	options = runtime.ConfigOptions()
	if got := optionCurrentValue(options, ConfigOptionProvider); got != "volcengine-agentplan" {
		t.Fatalf("provider after switch = %q", got)
	}
	if got := optionCurrentValue(options, ConfigOptionModel); got != "volcengine-agentplan/ark-code-latest" {
		t.Fatalf("model after switch = %q", got)
	}
	for _, option := range options {
		if option.ID == ConfigOptionModel {
			if len(option.Options) != 1 || option.Options[0].Value != "volcengine-agentplan/ark-code-latest" {
				t.Fatalf("cascaded model options = %#v", option.Options)
			}
		}
	}
	if entry, ok := manager.GetLatestModelChange(); !ok || entry.Provider != "volcengine-agentplan" || entry.ModelID != "ark-code-latest" {
		t.Fatalf("persisted provider/model = %#v, ok=%v", entry, ok)
	}
}

func optionCurrentValue(options []SessionConfigOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.CurrentValue
		}
	}
	return ""
}
