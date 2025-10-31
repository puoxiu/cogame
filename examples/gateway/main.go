package main

import (
	"crypto/md5"
	"fmt"
)


func hashPassword(password string) string {
	hash := md5.Sum([]byte(password + "co_game_salt"))
	return fmt.Sprintf("%x", hash)
}

func main() {
	t := "123456789"
	// 8ccd4fe51cfc4dc51f51b43625998ee5
	fmt.Println(hashPassword(t))
}