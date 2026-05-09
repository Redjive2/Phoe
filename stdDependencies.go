package main

import (
	"bufio"
	"fmt"
	"os"
)

type stdDependencies struct{}

var reader = bufio.NewReader(os.Stdin)

func (_ stdDependencies) ReadLine(prompt string) string {
	fmt.Print(prompt)

	answer, err := reader.ReadString('\n')
	if err != nil {
		panic(err)
	}

	return answer[:len(answer)-1]
}

func (_ stdDependencies) PrintLine(data ...any) {
	fmt.Println(data...)
}

func (_ stdDependencies) Print(data ...any) {
	fmt.Print(data...)
}

func (_ stdDependencies) Sprint(data ...any) string {
	return fmt.Sprint(data...)
}
