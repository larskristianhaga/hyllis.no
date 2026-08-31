// Package migrations embeds the SQL migration files in this directory so
// they're baked into the compiled binary and applied via bun/migrate at
// runtime. This matters for the production image: the Dockerfile's final
// stage only copies the compiled binary into a distroless image, not this
// directory, so a plain os.DirFS("migrations") read would find nothing
// there.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
