/*
This file is used to list dependencies we want to pull in manually.
Dependencies used in go code are automatically pulled, but some dependencies used by arbitrary build defs (think codegen) aren't so we add them here.
*/
package dummy

import (
	_ "github.com/bazelbuild/buildtools/build"
	_ "github.com/golang/protobuf/proto"
	_ "github.com/scylladb/go-set/strset"
	_ "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	_ "google.golang.org/genproto/googleapis/longrunning"
	_ "google.golang.org/protobuf/proto"
	_ "google.golang.org/protobuf/types/known/durationpb"
	_ "google.golang.org/protobuf/types/known/fieldmaskpb"
	_ "google.golang.org/protobuf/types/known/structpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
)
