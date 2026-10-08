package notion

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	rawIDPattern      = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	urlSegmentPattern = regexp.MustCompile(`(?:^|-)([0-9a-fA-F]{32})$`)
)

var errNoDatabaseID = errors.New("no Notion database ID found: enter a 32-character hex ID, a UUID, or a Notion database URL")

// ParseDatabaseID は Notion Database の URL か ID から Database ID を取り出し、ハイフン付き UUID の小文字で返す。
// URL では path の最後の segment の末尾 32 桁を使い、query の v=（view ID）は見ない。
func ParseDatabaseID(input string) (string, error) {
	s := strings.TrimSpace(input)
	if rawIDPattern.MatchString(s) || uuidPattern.MatchString(s) {
		return formatUUID(strings.ReplaceAll(s, "-", "")), nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errNoDatabaseID
	}
	segment := u.Path[strings.LastIndex(u.Path, "/")+1:]
	m := urlSegmentPattern.FindStringSubmatch(segment)
	if m == nil {
		return "", errNoDatabaseID
	}
	return formatUUID(m[1]), nil
}

func formatUUID(hex32 string) string {
	h := strings.ToLower(hex32)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
