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
		return []MarketClient{NewSkillHubClient("", nil), NewClawHubClient("", nil)}
	}
	clients := make([]MarketClient, 0, len(markets))
	for _, market := range markets {
		if !market.Enabled {
			continue
		}
		baseURL := strings.TrimSpace(market.APIURL)
		client := httpClientWithToken(market.APIToken)
		switch Market(market.ID) {
		case MarketSkillHub:
			clients = append(clients, NewSkillHubClient(baseURL, client))
		case MarketClawHub:
			clients = append(clients, NewClawHubClient(baseURL, client))
		}
	}
	return clients
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
