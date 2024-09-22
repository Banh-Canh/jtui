/*
Copyright © 2024 Victor Hang
*/
package main

import (
	"github.com/Banh-Canh/jtui/cmd"
	"github.com/Banh-Canh/jtui/internal/utils"
)

func main() {
	defer utils.SyncLogger()
	cmd.Execute()
}
