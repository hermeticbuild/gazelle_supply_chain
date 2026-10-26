// Uses Mattermost's AGPL server code, outside its enterprise/ directory.
package main

import (
	"fmt"

	"github.com/mattermost/mattermost/server/v8/platform/shared/mfa"
)

func main() {
	fmt.Println(mfa.New(nil))
}
