// Package settings provides read-only access to a small number of host settings required for toolkits similar to XSettings. It is not for general purpose settings.
// Upstream API documentation can be found at https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Settings.html.
package settings

import "github.com/rymdport/portal/internal/apis"

const settingsCallPath = apis.CallBaseName + ".Settings"
