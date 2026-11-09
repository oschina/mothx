package provider

func init() {
	RegisterVendorAdapter(simpleVendorAdapter{
		name:       "sensenova",
		domains:    []string{"sensenova.cn"},
		defaultAPI: "openai-chat",
	})
}
