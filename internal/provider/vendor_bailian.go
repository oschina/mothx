package provider

func init() {
	RegisterVendorAdapter(simpleVendorAdapter{
		name:    "bailian",
		domains: []string{"dashscope.aliyuncs.com", "token-plan.cn-beijing.maas.aliyuncs.com", "token-plan.maas.qianwenaiapi.com", "coding.dashscope.aliyuncs.com"},
	})
}
