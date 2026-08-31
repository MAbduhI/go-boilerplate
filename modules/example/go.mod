module github.com/MAbduhI/go-boilerplate/modules/example

go 1.26.5

// Sibling modules in this workspace (the shared module, other feature modules)
// are deliberately absent from this require block. go.work resolves them from
// disk; a require at a placeholder version would instead send the go command to
// the network looking for an unpublished tag.
require (
	github.com/gin-gonic/gin v1.12.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
	gorm.io/gorm v1.31.2
)
