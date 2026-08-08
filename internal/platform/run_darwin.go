package platform

import "fmt"

func Run(image string) error {
	fmt.Println("macOS host")
	fmt.Println("Will send container to Linux VM:", image)

	return nil
}
