package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
)

const defaultSpeedtestPrompt = "Reply with exactly 120 English words about terminal software performance. Do not use markdown, lists, or code."

type speedtestFlags struct {
	provider    string
	model       string
	prompt      string
	maxTokens   int
	timeout     time.Duration
	concurrency int
	runs        int
	thinking    string
}

type speedtestTarget struct {
	Provider  string
	ModelID   string
	ModelName string
}

type speedtestRequestOptions struct {
	Prompt        string
	MaxTokens     int
	ThinkingLevel provider.ThinkingLevel
}

type speedtestResult struct {
	Target            speedtestTarget
	TokensPerSecond   float64
	NetworkLatency    time.Duration
	FirstTokenLatency time.Duration
	TotalDuration     time.Duration
	OutputTokens      int
	EstimatedTokens   bool
	StopReason        string
	Error             error
}

func newSpeedtestCommand() *cobra.Command {
	flags := &speedtestFlags{
		prompt:      defaultSpeedtestPrompt,
		maxTokens:   256,
		timeout:     2 * time.Minute,
		concurrency: 1,
		runs:        3,
		thinking:    string(provider.ThinkingOff),
	}
	cmd := &cobra.Command{
		Use:   "speedtest",
		Short: "Benchmark configured providers and models",
		Long:  "Run a text-only streaming benchmark three times against configured provider/model pairs, average successful runs, and sort by output tokens per second.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSpeedtest(cmd.OutOrStdout(), cmd.ErrOrStderr(), flags)
		},
	}
	registerSpeedtestFlags(cmd.Flags(), flags)
	return cmd
}

func registerSpeedtestFlags(fs *pflag.FlagSet, flags *speedtestFlags) {
	fs.StringVarP(&flags.provider, "provider", "p", "", "Only test models from one provider")
	fs.StringVar(&flags.provider, "vendor", "", "Alias for --provider; only test models from one vendor")
	fs.StringVarP(&flags.model, "model", "m", "", "Only test one model ID")
	fs.StringVar(&flags.prompt, "prompt", defaultSpeedtestPrompt, "Text prompt used for every test")
	fs.IntVar(&flags.maxTokens, "max-tokens", 256, "Maximum output tokens per test request")
	fs.DurationVar(&flags.timeout, "timeout", 2*time.Minute, "Per-model timeout")
	fs.IntVar(&flags.concurrency, "concurrency", 1, "Number of models to test in parallel")
	fs.IntVar(&flags.runs, "runs", 3, "Number of runs per model (successful runs are averaged)")
	fs.StringVarP(&flags.thinking, "thinking", "t", string(provider.ThinkingOff), "Thinking level (off, minimal, low, medium, high, xhigh, max)")
}

func runSpeedtest(w, errw io.Writer, flags *speedtestFlags) error {
	if flags.maxTokens <= 0 {
		return fmt.Errorf("--max-tokens must be greater than 0")
	}
	if flags.timeout <= 0 {
		return fmt.Errorf("--timeout must be greater than 0")
	}
	if flags.concurrency <= 0 {
		return fmt.Errorf("--concurrency must be greater than 0")
	}
	if flags.runs <= 0 {
		return fmt.Errorf("--runs must be greater than 0")
	}
	thinkingLevel, err := parseSpeedtestThinkingLevel(flags.thinking)
	if err != nil {
		return err
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	targets := collectSpeedtestTargets(settings, flags)
	if len(targets) == 0 {
		return fmt.Errorf("no configured provider/model pairs found")
	}

	requestOpts := speedtestRequestOptions{
		Prompt:        flags.prompt,
		MaxTokens:     flags.maxTokens,
		ThinkingLevel: thinkingLevel,
	}
	fmt.Fprintf(errw, "Running text speedtest for %d model(s), %d run(s) each...\n", len(targets), flags.runs)
	results := runSpeedtestTargets(context.Background(), settings, targets, requestOpts, flags.timeout, flags.concurrency, flags.runs, errw)
	sortSpeedtestResults(results)
	if err := printSpeedtestResults(w, results); err != nil {
		return err
	}
	if countSpeedtestSuccesses(results) == 0 {
		return fmt.Errorf("all speedtest requests failed")
	}
	return nil
}

func parseSpeedtestThinkingLevel(level string) (provider.ThinkingLevel, error) {
	normalized := provider.ThinkingLevel(strings.TrimSpace(level))
	switch normalized {
	case provider.ThinkingOff, provider.ThinkingMinimal, provider.ThinkingLow, provider.ThinkingMedium, provider.ThinkingHigh, provider.ThinkingXHigh, provider.ThinkingMax:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid --thinking %q (use off, minimal, low, medium, high, xhigh, or max)", level)
	}
}

func collectSpeedtestTargets(settings *config.Settings, flags *speedtestFlags) []speedtestTarget {
	if settings == nil {
		return nil
	}
	var targets []speedtestTarget
	seen := map[string]bool{}
	for providerName, pc := range settings.Providers {
		if pc == nil {
			continue
		}
		if flags.provider != "" && providerName != flags.provider {
			continue
		}
		if !speedtestProviderConfigured(settings, providerName) {
			continue
		}

		for _, model := range pc.Models {
			if strings.TrimSpace(model.ID) == "" {
				continue
			}
			if flags.model != "" && model.ID != flags.model {
				continue
			}
			key := providerName + "\x00" + model.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, speedtestTarget{
				Provider:  providerName,
				ModelID:   model.ID,
				ModelName: model.Name,
			})
		}

		if len(pc.Models) == 0 {
			modelID := flags.model
			if modelID == "" && providerName == settings.DefaultProvider {
				modelID = settings.DefaultModel
			}
			if modelID == "" {
				continue
			}
			key := providerName + "\x00" + modelID
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, speedtestTarget{Provider: providerName, ModelID: modelID})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Provider != targets[j].Provider {
			return targets[i].Provider < targets[j].Provider
		}
		return targets[i].ModelID < targets[j].ModelID
	})
	return targets
}

func speedtestProviderConfigured(settings *config.Settings, providerName string) bool {
	if settings == nil {
		return false
	}
	if resolvedCredentialConfigured(settings.ResolveKey(providerName)) {
		return true
	}
	for _, value := range settings.ResolveProviderHeaders(providerName) {
		if resolvedCredentialConfigured(value) {
			return true
		}
	}
	return false
}

func resolvedCredentialConfigured(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return !(strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"))
}

func runSpeedtestTargets(ctx context.Context, settings *config.Settings, targets []speedtestTarget, opts speedtestRequestOptions, timeout time.Duration, concurrency, runs int, progress io.Writer) []speedtestResult {
	if concurrency > len(targets) {
		concurrency = len(targets)
	}
	targetCh := make(chan speedtestTarget)
	resultCh := make(chan speedtestResult)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range targetCh {
				p, model, err := providerfactory.Create(settings, target.Provider, target.ModelID)
				if err != nil {
					resultCh <- speedtestResult{Target: target, Error: err}
					continue
				}
				result := runSpeedtestRuns(ctx, p, model, target, opts, timeout, runs, settings)
				resultCh <- result
			}
		}()
	}

	go func() {
		for _, target := range targets {
			targetCh <- target
		}
		close(targetCh)
		wg.Wait()
		close(resultCh)
	}()

	results := make([]speedtestResult, 0, len(targets))
	for result := range resultCh {
		results = append(results, result)
		if progress != nil {
			printSpeedtestProgress(progress, result)
		}
	}
	return results
}

func runSpeedtestRuns(ctx context.Context, p provider.Provider, model *provider.Model, target speedtestTarget, opts speedtestRequestOptions, timeout time.Duration, runs int, settings *config.Settings) speedtestResult {
	results := make([]speedtestResult, 0, runs)
	for i := 0; i < runs; i++ {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		result := runSpeedtestRequest(runCtx, p, model, target, opts)
		cancel()
		result.NetworkLatency = measureSpeedtestNetworkLatency(ctx, settings, target.Provider)
		results = append(results, result)
	}
	return averageSpeedtestResults(results)
}

func averageSpeedtestResults(results []speedtestResult) speedtestResult {
	if len(results) == 0 {
		return speedtestResult{Error: fmt.Errorf("no speedtest runs")}
	}
	out := results[0]
	var tps, network, first, total float64
	var tokens, count int
	for _, result := range results {
		if result.Error != nil {
			continue
		}
		count++
		tps += result.TokensPerSecond
		network += float64(result.NetworkLatency)
		first += float64(result.FirstTokenLatency)
		total += float64(result.TotalDuration)
		tokens += result.OutputTokens
		out.StopReason = result.StopReason
		out.Error = nil
		out.EstimatedTokens = out.EstimatedTokens || result.EstimatedTokens
	}
	if count == 0 {
		return out
	}
	out.TokensPerSecond = tps / float64(count)
	out.NetworkLatency = time.Duration(network / float64(count))
	out.FirstTokenLatency = time.Duration(first / float64(count))
	out.TotalDuration = time.Duration(total / float64(count))
	out.OutputTokens = int(math.Round(float64(tokens) / float64(count)))
	return out
}

func measureSpeedtestNetworkLatency(ctx context.Context, settings *config.Settings, providerName string) time.Duration {
	if settings == nil {
		return 0
	}
	pc := settings.GetProviderConfig(providerName)
	if pc == nil {
		return 0
	}
	targetURL, err := url.Parse(pc.BaseURL)
	if err != nil || targetURL.Hostname() == "" {
		return 0
	}
	port := targetURL.Port()
	if port == "" {
		if targetURL.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	start := time.Now()
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(targetURL.Hostname(), port))
	if err != nil {
		return 0
	}
	_ = conn.Close()
	return time.Since(start)
}

func runSpeedtestRequest(ctx context.Context, p provider.Provider, model *provider.Model, target speedtestTarget, opts speedtestRequestOptions) speedtestResult {
	result := speedtestResult{Target: target}
	if model != nil {
		result.Target.ModelID = model.ID
		if result.Target.ModelName == "" {
			result.Target.ModelName = model.Name
		}
	}

	maxTokens := opts.MaxTokens
	if model != nil && model.MaxTokens > 0 && maxTokens > model.MaxTokens {
		maxTokens = model.MaxTokens
	}
	abort := make(chan struct{})
	defer close(abort)

	params := provider.ChatParams{
		Messages:      []provider.Message{provider.NewUserMessage(opts.Prompt)},
		ThinkingLevel: opts.ThinkingLevel,
		MaxTokens:     maxTokens,
		ModelID:       result.Target.ModelID,
		Abort:         abort,
	}
	if model != nil {
		params.Temperature = model.Temperature
		params.TopP = model.TopP
	}

	start := time.Now()
	stream := p.Chat(ctx, params)
	var firstTokenAt time.Time
	var output strings.Builder
	var usage *provider.Usage
	var streamErr error
	var stopReason string

	for ev := range stream {
		switch ev.Type {
		case provider.StreamTextDelta:
			if ev.TextDelta != "" && firstTokenAt.IsZero() {
				firstTokenAt = time.Now()
			}
			output.WriteString(ev.TextDelta)
		case provider.StreamThinkDelta:
			if ev.ThinkDelta != "" && firstTokenAt.IsZero() {
				firstTokenAt = time.Now()
			}
			output.WriteString(ev.ThinkDelta)
		case provider.StreamUsage:
			if ev.Usage != nil {
				usage = ev.Usage
			}
		case provider.StreamDone:
			stopReason = ev.StopReason
		case provider.StreamError:
			streamErr = ev.Error
			if ev.StopReason != "" {
				stopReason = ev.StopReason
			}
		}
	}
	end := time.Now()

	result.StopReason = stopReason
	result.TotalDuration = end.Sub(start)
	if !firstTokenAt.IsZero() {
		result.FirstTokenLatency = firstTokenAt.Sub(start)
	}
	result.OutputTokens, result.EstimatedTokens = speedtestOutputTokens(usage, output.String())
	if streamErr != nil {
		result.Error = streamErr
		return result
	}
	if firstTokenAt.IsZero() {
		result.Error = fmt.Errorf("no streamed text tokens received")
		return result
	}
	if result.OutputTokens <= 0 {
		result.Error = fmt.Errorf("no output tokens measured")
		return result
	}
	generationDuration := end.Sub(firstTokenAt)
	if generationDuration <= 0 {
		generationDuration = time.Millisecond
	}
	result.TokensPerSecond = float64(result.OutputTokens) / generationDuration.Seconds()
	return result
}

func speedtestOutputTokens(usage *provider.Usage, output string) (int, bool) {
	if usage != nil && usage.Output > 0 {
		return usage.Output, false
	}
	return estimateSpeedtestTokens(output), true
}

func estimateSpeedtestTokens(output string) int {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0
	}
	words := len(strings.Fields(output))
	runes := utf8.RuneCountInString(output)
	byRunes := int(math.Ceil(float64(runes) / 4.0))
	if words > byRunes {
		return words
	}
	return byRunes
}

func sortSpeedtestResults(results []speedtestResult) {
	sort.SliceStable(results, func(i, j int) bool {
		iOK := results[i].Error == nil
		jOK := results[j].Error == nil
		if iOK != jOK {
			return iOK
		}
		if iOK && results[i].TokensPerSecond != results[j].TokensPerSecond {
			return results[i].TokensPerSecond > results[j].TokensPerSecond
		}
		if results[i].Target.Provider != results[j].Target.Provider {
			return results[i].Target.Provider < results[j].Target.Provider
		}
		return results[i].Target.ModelID < results[j].Target.ModelID
	})
}

func countSpeedtestSuccesses(results []speedtestResult) int {
	count := 0
	for _, result := range results {
		if result.Error == nil {
			count++
		}
	}
	return count
}

func printSpeedtestProgress(w io.Writer, result speedtestResult) {
	name := result.Target.Provider + "/" + result.Target.ModelID
	if result.Error != nil {
		fmt.Fprintf(w, "err %s: %s\n", name, shortSpeedtestError(result.Error))
		return
	}
	fmt.Fprintf(w, "ok  %s: %s token/s, network %s, first token %s\n",
		name,
		formatSpeedtestRate(result.TokensPerSecond),
		formatSpeedtestDuration(result.NetworkLatency),
		formatSpeedtestDuration(result.FirstTokenLatency),
	)
}

func printSpeedtestResults(w io.Writer, results []speedtestResult) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Provider\tModel\tToken/s\tNetwork\tFirst token\tTotal\tOutput\tStatus")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.Target.Provider,
			result.Target.ModelID,
			formatSpeedtestRate(result.TokensPerSecond),
			formatSpeedtestDuration(result.NetworkLatency),
			formatSpeedtestDuration(result.FirstTokenLatency),
			formatSpeedtestDuration(result.TotalDuration),
			formatSpeedtestOutput(result.OutputTokens, result.EstimatedTokens),
			formatSpeedtestStatus(result),
		)
	}
	return tw.Flush()
}

func formatSpeedtestRate(rate float64) string {
	if rate <= 0 {
		return "--"
	}
	return fmt.Sprintf("%.1f", rate)
}

func formatSpeedtestDuration(d time.Duration) string {
	if d <= 0 {
		return "--"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Round(time.Millisecond)/time.Millisecond)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func formatSpeedtestOutput(tokens int, estimated bool) string {
	if tokens <= 0 {
		return "--"
	}
	if estimated {
		return fmt.Sprintf("~%d", tokens)
	}
	return fmt.Sprintf("%d", tokens)
}

func formatSpeedtestStatus(result speedtestResult) string {
	if result.Error != nil {
		return shortSpeedtestError(result.Error)
	}
	if result.StopReason != "" {
		return result.StopReason
	}
	return "ok"
}

func shortSpeedtestError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	const limit = 120
	if len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
}
