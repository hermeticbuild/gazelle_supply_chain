// Uses code from both outside and inside BuildBuddy's enterprise/ directory.
package main

import (
	"fmt"

	"github.com/buildbuddy-io/buildbuddy/enterprise/server/util/fieldgetter"
	"github.com/buildbuddy-io/buildbuddy/server/util/random"
)

func main() {
	fmt.Println(random.RandomString(8))
	fmt.Println(fieldgetter.ExtractValues(struct{ A string }{"a"}, "A"))
}
