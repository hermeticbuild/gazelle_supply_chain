// Uses only code outside BuildBuddy's enterprise/ directory.
package main

import (
	"fmt"

	"github.com/buildbuddy-io/buildbuddy/server/util/random"
)

func main() {
	fmt.Println(random.RandomString(8))
}
