// SPDX-License-Identifier: Apache-2.0

// Command wuzuo is an AI forensic examiner for production incidents.
package main

import (
	"os"

	"github.com/yinx0004/wuzuo/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
