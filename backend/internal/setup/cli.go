// Package setup provides CLI commands and application initialization helpers.
package setup

import (
	"bufio"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/appdata"
	"golang.org/x/term"
)

func cliValidateDBName(name string) bool {
	validName := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
	return validName.MatchString(name) && len(name) <= 63
}

func cliValidateEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil && len(email) <= 254
}

func cliValidatePort(port int) bool {
	return port > 0 && port <= 65535
}

func cliValidateSSLMode(mode string) bool {
	validModes := map[string]bool{
		"disable": true, "require": true, "verify-ca": true, "verify-full": true,
	}
	return validModes[mode]
}

// RunCLI runs the CLI setup wizard
func RunCLI() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════╗")
	fmt.Println("║       Sub2API Installation Wizard         ║")
	fmt.Println("╚═══════════════════════════════════════════╝")
	fmt.Println()

	cfg := &SetupConfig{
		Database: DatabaseConfig{
			Engine:  "sqlite",
			DBName:  appdata.DefaultSQLiteDBPath(),
			SSLMode: "disable",
		},
		Redis: RedisConfig{
			Enabled: false,
		},
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
			Mode: "release",
		},
		JWT: JWTConfig{
			ExpireHour: 24,
		},
	}

	// Database configuration with validation
	fmt.Println("── Database Configuration ──")
	cfg.Database.Engine = "sqlite"
	cfg.Database.DBName = promptString(reader, "SQLite Database Path", appdata.DefaultSQLiteDBPath())
	cfg.Database.DBName = strings.TrimSpace(cfg.Database.DBName)
	if cfg.Database.DBName == "" {
		cfg.Database.DBName = appdata.DefaultSQLiteDBPath()
	}
	cfg.Database.SSLMode = "disable"

	fmt.Println()
	fmt.Print("Testing database connection... ")
	if err := TestDatabaseConnection(&cfg.Database); err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("database connection failed: %w", err)
	}
	fmt.Println("OK")

	// Admin configuration with validation
	fmt.Println()
	fmt.Println("── Admin Account ──")

	for {
		cfg.Admin.Email = promptString(reader, "Admin Email", "admin@example.com")
		if cliValidateEmail(cfg.Admin.Email) {
			break
		}
		fmt.Println("  Invalid email format.")
	}

	for {
		cfg.Admin.Password = promptPassword("Admin Password")
		// SECURITY: Match Web API requirement of 8 characters minimum
		if len(cfg.Admin.Password) < 8 {
			fmt.Println("  Password must be at least 8 characters")
			continue
		}
		if len(cfg.Admin.Password) > 128 {
			fmt.Println("  Password must be at most 128 characters")
			continue
		}
		confirm := promptPassword("Confirm Password")
		if cfg.Admin.Password != confirm {
			fmt.Println("  Passwords do not match")
			continue
		}
		break
	}

	// Server configuration with validation
	fmt.Println()
	fmt.Println("── Server Configuration ──")

	for {
		cfg.Server.Port = promptInt(reader, "Server Port", 8080)
		if cliValidatePort(cfg.Server.Port) {
			break
		}
		fmt.Println("  Invalid port. Must be between 1 and 65535.")
	}

	// Confirm and install
	fmt.Println()
	fmt.Println("── Configuration Summary ──")
	fmt.Printf("Database: SQLite (%s)\n", cfg.Database.DBName)
	fmt.Printf("Redis: disabled\n")
	fmt.Printf("Admin: %s\n", cfg.Admin.Email)
	fmt.Printf("Server: :%d\n", cfg.Server.Port)
	fmt.Println()

	if !promptConfirm(reader, "Proceed with installation?") {
		fmt.Println("Installation cancelled")
		return nil
	}

	fmt.Println()
	fmt.Print("Installing... ")
	if err := Install(cfg); err != nil {
		fmt.Println("FAILED")
		return err
	}
	fmt.Println("OK")

	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════╗")
	fmt.Println("║       Installation Complete!              ║")
	fmt.Println("╚═══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Start the server with:")
	fmt.Println("  ./sub2api")
	fmt.Println()
	fmt.Printf("Admin panel: http://localhost:%d\n", cfg.Server.Port)
	fmt.Println()

	return nil
}

func promptString(reader *bufio.Reader, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("  %s: ", prompt)
	}

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return defaultVal
	}
	return input
}

func promptInt(reader *bufio.Reader, prompt string, defaultVal int) int {
	fmt.Printf("  %s [%d]: ", prompt, defaultVal)

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return defaultVal
	}

	val, err := strconv.Atoi(input)
	if err != nil {
		return defaultVal
	}
	return val
}

func promptPassword(prompt string) string {
	fmt.Printf("  %s: ", prompt)

	// Try to read password without echo
	if term.IsTerminal(int(os.Stdin.Fd())) {
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			return string(password)
		}
	}

	// Fallback to regular input
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}

func promptConfirm(reader *bufio.Reader, prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
