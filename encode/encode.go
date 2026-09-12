package encode

import "strings"

func Encode(id int64) string {
	var url_encoded strings.Builder
	for id > 0 {
		remainder := id % 62
		if remainder < 10 {
			url_encoded.WriteByte('0' + byte(remainder))
		} else {
			remainder -= 10
			divised := remainder / 26
			// it's above 26, select A-Z for encode
			if divised != 0 {
				url_encoded.WriteByte(byte('A' + remainder%26))
			} else {
				// select a - z
				url_encoded.WriteByte(byte('a' + remainder%26))
			}
		}
		id /= 62
	}
	return url_encoded.String()
}
