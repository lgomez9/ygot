#!/bin/bash

# Hack to ensure that if we are running on OS X with a homebrew installed
# GNU sed then we can still run sed.
runsed() {
  if hash gsed 2>/dev/null; then
    gsed "$@"
  else
    sed "$@"
  fi
}

go run ../../../generator/generator.go -path="." -output_file=utestschema.go \
  -package_name=utestschema -generate_fakeroot -fakeroot_name=device \
  -shorten_enum_leaf_names \
  -typedef_enum_with_defmod \
  -enum_suffix_for_simple_union_enums \
  -generate_rename \
  -generate_append \
  -generate_getters \
  -generate_leaf_getters \
  -generate_simple_unions \
  -annotations \
  ../yang/utestschema.yang \
  ../yang/refschema.yang \
  ../yang/ctestschema.yang
gofmt -w -s utestschema.go
