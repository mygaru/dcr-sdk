// Command proto-verify loads every generated descriptor and prints what it
// registered.
//
// A corrupted FileDescriptorProto does not fail the build: the *.pb.go still
// compiles and only panics when the package init parses the embedded raw
// descriptor at run time. That is how a sed-rewritten module path shipped to
// consumers twice, panicking with "slice bounds out of range" inside
// filedesc.(*File).unmarshalSeed. Importing gen/base1 here turns that run-time
// panic into a `make proto` failure.
package main

import (
	"fmt"
	"os"

	base "github.com/mygaru/dcr-sdk/gen/base1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	// Reaching this line already proves every descriptor parsed: the panic would
	// have happened in the package init of gen/base1 above.
	files := []protoreflect.FileDescriptor{
		base.File_base_v1_common_proto,
		base.File_base_v1_frequency_proto,
		base.File_base_v1_match_proto,
		base.File_base_v1_rpc_report_proto,
		base.File_base_v1_rpc_target_proto,
		base.File_base_v1_user_proto,
	}

	for _, fd := range files {
		if fd == nil {
			fmt.Fprintln(os.Stderr, "proto-verify: nil file descriptor")
			os.Exit(1)
		}

		// Re-resolving through the global registry catches a descriptor that
		// parsed but registered under an unexpected path.
		if _, err := protoregistry.GlobalFiles.FindFileByPath(fd.Path()); err != nil {
			fmt.Fprintf(os.Stderr, "proto-verify: %s is not registered: %v\n", fd.Path(), err)
			os.Exit(1)
		}

		// Reading the options forces the lazy, full descriptor unmarshal - a
		// stricter check than the eager pass done at init - and shows the
		// go_package the mirror must not have to rewrite.
		opts, _ := fd.Options().(*descriptorpb.FileOptions)
		fmt.Printf("  %-28s package=%-10s go_package=%s\n",
			fd.Path(), fd.Package(), opts.GetGoPackage())
	}
}
