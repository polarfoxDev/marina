package helpers

import (
	"fmt"
	"strings"
)

func ValidateCron(c string) error {
	if strings.Count(c, " ") < 4 {
		return fmt.Errorf("cron expression too short: %q", c)
	}
	return nil
}
