package skillhub

import (
	"net/http"
	"strings"

	"github.com/oschina/mothx/internal/config"
)

// ClientsForSettings constructs the enabled built-in market clients from settings.
// Custom IDs are reserved for future adapters and are ignored rather than silently
// treating an incompatible API as SkillHub or ClawHub.
func ClientsForSettings(settings config.SkillHubSettings) []MarketClient {
	markets := settings.Markets
	if len(markets) == 0 {
		return defaultMarketClients()
	}
	clients := make([]MarketClient, 0, len(markets))
	for _, market := range markets {
		if !market.Enabled {
			continue
		}
		baseURL := strings.TrimSpace(market.APIURL)
		client := httpClientWithToken(market.APIToken)
		switch Market(market.ID) {
		case MarketYuandun:
			clients = append(clients, NewYuandunClient(baseURL, client))
		case MarketSkillHub:
			clients = append(clients, NewSkillHubClient(baseURL, client))
		case MarketClawHub:
			clients = append(clients, NewClawHubClient(baseURL, client))
		}
	}
	return clients
}

func defaultMarketClients() []MarketClient {
	return []MarketClient{
		NewYuandunClient("", nil),
		NewSkillHubClient("", nil),
		NewClawHubClient("", nil),
	}
}

// MarketAvailable reports whether a market can actually be used with the current
// settings. Every built-in market has a default deployment origin, so an enabled
// entry is usable unless a caller explicitly disables it.
func MarketAvailable(settings config.SkillHubSettings, market Market) bool {
	if len(settings.Markets) == 0 {
		return market == MarketYuandun || market == MarketSkillHub || market == MarketClawHub
	}
	for _, entry := range settings.Markets {
		if !entry.Enabled || Market(entry.ID) != market {
			continue
		}
		return true
	}
	return false
}

func hasMarket(markets []config.SkillHubMarketSettings, market Market) bool {
	for _, entry := range markets {
		if Market(entry.ID) == market {
			return true
		}
	}
	return false
}

// ResolveDefaultMarket applies the configured preference, then falls back to the
// highest-priority enabled market.
func ResolveDefaultMarket(settings config.SkillHubSettings) Market {
	preferred := Market(strings.TrimSpace(settings.DefaultMarket))
	if preferred == MarketSkillHub && (len(settings.Markets) == 0 || hasMarket(settings.Markets, MarketYuandun)) {
		// skillhub.cn was the implicit pre-源盾 product default. Migrate it
		// unless the user has a custom market list without 源盾.
		preferred = MarketYuandun
	}
	if preferred != "" && MarketAvailable(settings, preferred) {
		return preferred
	}
	for _, market := range []Market{MarketYuandun, MarketSkillHub, MarketClawHub} {
		if MarketAvailable(settings, market) {
			return market
		}
	}
	return MarketSkillHub
}

type tokenTransport struct {
	token string
	next  http.RoundTripper
}

func (t tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(clone)
}
func httpClientWithToken(token string) *http.Client {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return &http.Client{Timeout: defaultRequestTimeout, Transport: tokenTransport{token: token, next: http.DefaultTransport}}
}
