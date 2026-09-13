// Command createsuperuser creates the first superuser straight in the
// database. It is the cold-start path for an empty installation: no API
// endpoint can hand out superuser rights to the first account.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

func main() {
	config.InitDatabase()
	if err := config.DB.AutoMigrate(&repository.User{}, &repository.Group{}, &repository.UserGroup{}); err != nil {
		log.Fatalf("createsuperuser: automigrate: %v", err)
	}

	input := bufio.NewReader(os.Stdin)

	username := prompt(input, "Username: ")
	email := prompt(input, "Email: ")
	password := prompt(input, "Password: ")
	confirmation := prompt(input, "Password (again): ")

	if password != confirmation {
		log.Fatal("createsuperuser: the two passwords do not match")
	}

	attrs := utils.Attributes{Username: username, Email: email}
	if violations := utils.ValidatePassword(password, attrs); len(violations) > 0 {
		for _, violation := range violations {
			fmt.Fprintf(os.Stderr, "createsuperuser: %s\n", violation.Message)
		}
		os.Exit(1)
	}

	hash, err := utils.HashPassword(password)
	if err != nil {
		log.Fatalf("createsuperuser: hash password: %v", err)
	}

	user := &repository.User{
		ID:             uuid.New(),
		Username:       username,
		Email:          email,
		HashedPassword: hash,
		IsActive:       true,
		IsSuperuser:    true,
	}
	if err := repository.CreateUser(context.Background(), config.DB, user); err != nil {
		log.Fatalf("createsuperuser: create user: %v", err)
	}

	fmt.Printf("Superuser %s created.\n", user.Username)
}

// prompt writes label and reads one line from stdin.
func prompt(input *bufio.Reader, label string) string {
	fmt.Print(label)
	line, err := input.ReadString('\n')
	if err != nil && line == "" {
		log.Fatalf("createsuperuser: cannot read %s: %v", strings.TrimSuffix(label, ": "), err)
	}
	return strings.TrimSpace(line)
}
