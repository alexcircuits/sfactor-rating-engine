// Package ubki parses the fields used by the rating engine from УБКІ XML template 10.
//
// Reports may contain empty fields or placeholder dates such as 1900-01-01. Invalid
// attribute values become zero or unknown; malformed XML returns an error.
package ubki
