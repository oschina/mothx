package desktop

import "github.com/oschina/mothx/native/internal/i18n"

// Locale and translation entry points are owned by the private internal/i18n
// package. The shell keeps short aliases so existing rendering call sites stay
// focused on UI structure instead of import noise.
var (
	T               = i18n.T
	Tf              = i18n.Tf
	CurrentLocale   = i18n.CurrentLocale
	SetLocale       = i18n.SetLocale
	SetNativeLocale = i18n.SetNativeLocale
	LocalizedError  = i18n.LocalizedError
	LocalizedErrorf = i18n.LocalizedErrorf
)
