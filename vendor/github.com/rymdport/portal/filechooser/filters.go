package filechooser

// FilterType is the type of filter to use.
type FilterType = uint32

const (
	GlobPattern FilterType = iota // Use a glob-style pattern for matching.
	MIMEType                      // Use a MIME type for matching.
)

// Rule is used to specify a filter rule.
type Rule struct {
	Type    FilterType // The type of filter to use.
	Pattern string     // The pattern rules are case-sensitive.
}

// Filter specifies a filter containing various rules for allowed files.
type Filter struct {
	Name  string // User-visible name for the filter.
	Rules []Rule // The list rules that apply for this filter.
}
