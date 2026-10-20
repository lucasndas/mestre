package ghcli

// plainOpID says an op_id is only letters, digits and . _ : -, so it can go into
// the text of an inbox item without bringing a line break or a header of its own.
func plainOpID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}
