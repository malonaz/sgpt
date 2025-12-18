package chat

import (
	"log"
	"os"
)

// At the start of your program or in init()
func init() {
	f, err := os.OpenFile("/tmp/sgpt-debug.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}
	log.SetOutput(f)
}
