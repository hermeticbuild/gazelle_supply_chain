// Uses only code outside BuildBuddy's enterprise/ directory.
package main

import (
	"fmt"

	"github.com/buildbuddy-io/buildbuddy/server/util/random"
	"github.com/buildbuddy-io/buildbuddy/server/util/status"
)

func main() {
	str, _ := random.RandomString(8)
	fmt.Println(str, status.NotFoundError("example"))
}
