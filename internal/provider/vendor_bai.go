package provider

func init() {
	RegisterVendorAdapter(simpleVendorAdapter{
		name:    "bai",
		domains: []string{"api.b.ai"},
	})
}
