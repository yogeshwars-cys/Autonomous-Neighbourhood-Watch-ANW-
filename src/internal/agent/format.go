package agent

import "strconv"

func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}
