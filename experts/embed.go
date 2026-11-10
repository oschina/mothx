// Package experts embeds the built-in expert bundle seed packages shipped
// with the binary. internal/expert consumes BuiltinFS as the lowest-priority
// (builtin) ExpertCenter source.
package experts

import "embed"

// BuiltinFS contains the embedded seed expert bundles. Each top-level
// directory is one bundle (expert.json + agents/*.md [+ skills/ avatars/]).
//
// The catalog is deliberately role-based: software-company and
// frontend-developer are the original generalists, while the remaining
// bundles split common engineering and operations domains into small teams
// with explicit leaders and bounded member roles.
//
//go:embed all:software-company all:frontend-developer
//go:embed all:backend-platform all:web-frontend all:mobile-crossplatform
//go:embed all:systems-lowlevel all:enterprise-jvm-dotnet all:qa-testing
//go:embed all:security-compliance all:devops-sre all:data-ml-ai
//go:embed all:product-architecture all:docs-research all:agent-platform
var BuiltinFS embed.FS
