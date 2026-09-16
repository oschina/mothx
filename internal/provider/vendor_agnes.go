package provider

func init() {
	RegisterVendorAdapter(simpleVendorAdapter{
		name:    "agnes",
		domains: []string{"apihub.agnes-ai.com", "api.agnes-ai.cn"},
	})
}
