// Package names holds the test that every package's exported format
// name is internal/formats' table's. It is a package of its own so that
// the test's imports (convert, and with it the OTel SDK) stay out of the
// test graph of every package that imports internal/formats, which a
// user module's go mod tidy would otherwise record.
package names
