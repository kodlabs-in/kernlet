package platform

import (
	"fmt"
)

func Run(image string) error {
	fmt.Println("Linux runtime requested for:", image)

	return nil
}
