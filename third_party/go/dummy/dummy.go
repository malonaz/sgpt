/*
This file is used to list dependencies we want to pull in manually.
Dependencies used in go code are automatically pulled, but some dependencies used by arbitrary build defs (think codegen) aren't so we add them here.
*/
package dummy

import (
	_ "github.com/bazelbuild/buildtools/build"
	_ "github.com/scylladb/go-set/strset"
)
