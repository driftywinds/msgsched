package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

type Schedule struct {
	ID          int
	UserID      string
	Title       string
	Message     string
	ChannelID   string
	IsRepeating bool
	RepeatType  string
	RepeatValue string
	NextRun     time.Time
	CreatedAt   time.Time
}

type User struct {
	UserID   string
	Timezone string
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN not set in environment")
	}

	var err error
	db, err = initDB()
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer db.Close()

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}

	dg.AddHandler(ready)
	dg.AddHandler(interactionCreate)

	dg.Identify.Intents = discordgo.IntentsGuilds

	if err := dg.Open(); err != nil {
		log.Fatal("Error opening connection:", err)
	}

	log.Println("Registering commands...")
	registerCommands(dg)

	go scheduleRunner(dg)

	log.Println("Bot is running. Press CTRL+C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	dg.Close()
}

func initDB() (*sql.DB, error) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./schedules.db"
	}
	
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	createSchedulesTable := `
	CREATE TABLE IF NOT EXISTS schedules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		title TEXT NOT NULL,
		message TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		is_repeating BOOLEAN NOT NULL,
		repeat_type TEXT,
		repeat_value TEXT,
		next_run DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		user_id TEXT PRIMARY KEY,
		timezone TEXT DEFAULT 'UTC'
	);`

	if _, err = db.Exec(createSchedulesTable); err != nil {
		return nil, err
	}
	if _, err = db.Exec(createUsersTable); err != nil {
		return nil, err
	}

	return db, nil
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
}

func registerCommands(s *discordgo.Session) {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "help",
			Description: "Show help information about the scheduler bot",
		},
		{
			Name:        "create_schedule",
			Description: "Create a new message schedule",
		},
		{
			Name:        "list_schedules",
			Description: "List all your schedules",
		},
		{
			Name:        "delete_schedule",
			Description: "Delete a schedule by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID to delete",
					Required:    true,
				},
			},
		},
		{
			Name:        "test_schedule",
			Description: "Test a schedule by sending it immediately",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID to test",
					Required:    true,
				},
			},
		},
		{
			Name:        "set_timezone",
			Description: "Set your timezone for schedule times",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "timezone",
					Description: "Timezone (e.g., America/New_York, Europe/London, Asia/Kolkata)",
					Required:    true,
				},
			},
		},
		{
			Name:        "get_timezone",
			Description: "Check your current timezone setting",
		},
	}

	for _, cmd := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", cmd)
		if err != nil {
			log.Printf("Cannot create '%v' command: %v", cmd.Name, err)
		}
	}
}

func interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionApplicationCommand {
		switch i.ApplicationCommandData().Name {
		case "help":
			handleHelp(s, i)
		case "create_schedule":
			handleCreateSchedule(s, i)
		case "list_schedules":
			handleListSchedules(s, i)
		case "delete_schedule":
			handleDeleteSchedule(s, i)
		case "test_schedule":
			handleTestSchedule(s, i)
		case "set_timezone":
			handleSetTimezone(s, i)
		case "get_timezone":
			handleGetTimezone(s, i)
		}
	} else if i.Type == discordgo.InteractionModalSubmit {
		handleModalSubmit(s, i)
	}
}

func handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	helpText := "**📅 Schedule Bot Help**\n\n" +
		"**Commands:**\n" +
		"• `/create_schedule` - Create a new message schedule\n" +
		"• `/list_schedules` - View all your schedules\n" +
		"• `/delete_schedule` - Delete a schedule by ID\n" +
		"• `/test_schedule` - Test a schedule by sending it immediately\n" +
		"• `/set_timezone` - Set your timezone for interpreting times\n" +
		"• `/get_timezone` - Check your current timezone\n" +
		"• `/help` - Show this help message\n\n" +
		"**How it works:**\n" +
		"1. Set your timezone with `/set_timezone`\n" +
		"2. Use `/create_schedule` to start\n" +
		"3. Fill in the modal with your schedule details\n" +
		"4. Test it with `/test_schedule` if you want\n" +
		"5. The bot will send your message at the scheduled time!\n\n" +
		"**Repeat Options:**\n" +
		"• **None** - Send once only\n" +
		"• **Interval** - Repeat every X minutes (e.g., \"60\" for hourly)\n" +
		"• **Days** - Repeat on specific days (e.g., \"Mon,Wed,Fri\")\n\n" +
		"**Time Format:**\n" +
		"Use format: `2006-01-02 15:04` (YYYY-MM-DD HH:MM in your timezone)\n" +
		"Example: `2024-12-25 09:00`\n\n" +
		"**Common Timezones:**\n" +
		"• America/New_York, America/Los_Angeles\n" +
		"• Europe/London, Europe/Paris\n" +
		"• Asia/Kolkata, Asia/Tokyo\n" +
		"• Australia/Sydney"

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: helpText,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleSetTimezone(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	timezone := options[0].StringValue()

	// Validate timezone
	_, err := time.LoadLocation(timezone)
	if err != nil {
		respondError(s, i, "Invalid timezone. Use format like 'America/New_York' or 'Asia/Kolkata'")
		return
	}

	_, err = db.Exec("INSERT OR REPLACE INTO users (user_id, timezone) VALUES (?, ?)",
		i.Member.User.ID, timezone)
	if err != nil {
		respondError(s, i, "Failed to set timezone")
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ Timezone set to **%s**", timezone),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleGetTimezone(s *discordgo.Session, i *discordgo.InteractionCreate) {
	timezone := getUserTimezone(i.Member.User.ID)
	currentTime := time.Now().In(getLocation(timezone))

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("🌍 Your timezone: **%s**\nCurrent time: **%s**",
				timezone, currentTime.Format("2006-01-02 15:04:05")),
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

func getUserTimezone(userID string) string {
	var timezone string
	err := db.QueryRow("SELECT timezone FROM users WHERE user_id = ?", userID).Scan(&timezone)
	if err != nil {
		return "UTC"
	}
	return timezone
}

func getLocation(timezone string) *time.Location {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc, _ = time.LoadLocation("UTC")
	}
	return loc
}

func handleCreateSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	timezone := getUserTimezone(i.Member.User.ID)
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "schedule_modal",
			Title:    "Create New Schedule",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "title",
							Label:       "Title",
							Style:       discordgo.TextInputShort,
							Placeholder: "My Schedule",
							Required:    true,
							MaxLength:   100,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "message",
							Label:       "Message",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "The message to send",
							Required:    true,
							MaxLength:   2000,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "channel_id",
							Label:       "Channel ID",
							Style:       discordgo.TextInputShort,
							Placeholder: "Right-click channel > Copy ID",
							Required:    true,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "first_run",
							Label:       fmt.Sprintf("First Run (YYYY-MM-DD HH:MM in %s)", timezone),
							Style:       discordgo.TextInputShort,
							Placeholder: "2024-12-25 09:00",
							Required:    true,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "repeat",
							Label:       "Repeat: none/interval:60/days:Mon,Wed,Fri",
							Style:       discordgo.TextInputShort,
							Placeholder: "none",
							Required:    true,
						},
					},
				},
			},
		},
	})

	if err != nil {
		log.Println("Error showing modal:", err)
	}
}

func handleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	title := data.Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	message := data.Components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	channelID := data.Components[2].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	firstRunStr := data.Components[3].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	repeatStr := data.Components[4].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value

	// Get user's timezone
	timezone := getUserTimezone(i.Member.User.ID)
	loc := getLocation(timezone)

	// Parse time in user's timezone
	firstRun, err := time.ParseInLocation("2006-01-02 15:04", firstRunStr, loc)
	if err != nil {
		respondError(s, i, "Invalid time format. Use: YYYY-MM-DD HH:MM")
		return
	}

	// Convert to UTC for storage
	firstRunUTC := firstRun.UTC()

	isRepeating := false
	repeatType := "none"
	repeatValue := ""

	if repeatStr != "none" {
		isRepeating = true
		parts := strings.SplitN(repeatStr, ":", 2)
		if len(parts) == 2 {
			repeatType = parts[0]
			repeatValue = parts[1]
		} else {
			respondError(s, i, "Invalid repeat format. Use: none, interval:60, or days:Mon,Wed,Fri")
			return
		}
	}

	_, err = db.Exec(`
		INSERT INTO schedules (user_id, title, message, channel_id, is_repeating, repeat_type, repeat_value, next_run)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		i.Member.User.ID, title, message, channelID, isRepeating, repeatType, repeatValue, firstRunUTC)

	if err != nil {
		respondError(s, i, "Failed to create schedule: "+err.Error())
		return
	}

	// Get username for logging
	username := i.Member.User.Username
	if i.Member.User.GlobalName != "" {
		username = i.Member.User.GlobalName
	}

	log.Printf("📅 NEW SCHEDULE CREATED by %s (@%s):\n  Title: %s\n  Channel: %s\n  First Run: %s (%s)\n  Repeat: %s\n",
		username, i.Member.User.Username, title, channelID, firstRun.Format("2006-01-02 15:04"), timezone, repeatStr)

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ Schedule **%s** created successfully!\nFirst run: %s (%s)\nUse `/test_schedule` to test it!",
				title, firstRun.Format("2006-01-02 15:04"), timezone),
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleListSchedules(s *discordgo.Session, i *discordgo.InteractionCreate) {
	timezone := getUserTimezone(i.Member.User.ID)
	loc := getLocation(timezone)

	rows, err := db.Query(`
		SELECT id, title, message, channel_id, is_repeating, repeat_type, repeat_value, next_run
		FROM schedules WHERE user_id = ? ORDER BY next_run`, i.Member.User.ID)
	if err != nil {
		respondError(s, i, "Failed to fetch schedules")
		return
	}
	defer rows.Close()

	var schedules []string
	for rows.Next() {
		var id int
		var title, message, channelID, repeatType, repeatValue string
		var isRepeating bool
		var nextRunUTC time.Time

		rows.Scan(&id, &title, &message, &channelID, &isRepeating, &repeatType, &repeatValue, &nextRunUTC)

		// Convert to user's timezone
		nextRun := nextRunUTC.In(loc)

		repeatInfo := "One-time"
		if isRepeating {
			if repeatType == "interval" {
				repeatInfo = fmt.Sprintf("Every %s minutes", repeatValue)
			} else if repeatType == "days" {
				repeatInfo = fmt.Sprintf("Days: %s", repeatValue)
			}
		}

		schedules = append(schedules, fmt.Sprintf(
			"**ID: %d** | %s\nNext: %s | %s\nChannel: <#%s>",
			id, title, nextRun.Format("2006-01-02 15:04"), repeatInfo, channelID))
	}

	content := fmt.Sprintf("**📅 Your Schedules** (Timezone: %s)\n\n", timezone)
	if len(schedules) == 0 {
		content += "No schedules found. Use `/create_schedule` to create one!"
	} else {
		content += strings.Join(schedules, "\n\n")
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleDeleteSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	scheduleID := options[0].IntValue()

	result, err := db.Exec("DELETE FROM schedules WHERE id = ? AND user_id = ?", scheduleID, i.Member.User.ID)
	if err != nil {
		respondError(s, i, "Failed to delete schedule")
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		respondError(s, i, "Schedule not found or you don't have permission to delete it")
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ Schedule #%d deleted successfully!", scheduleID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleTestSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	scheduleID := options[0].IntValue()

	var title, message, channelID string
	err := db.QueryRow("SELECT title, message, channel_id FROM schedules WHERE id = ? AND user_id = ?",
		scheduleID, i.Member.User.ID).Scan(&title, &message, &channelID)

	if err != nil {
		respondError(s, i, "Schedule not found or you don't have permission to test it")
		return
	}

	// Send test message
	testMessage := fmt.Sprintf("**🧪 TEST MESSAGE for: %s**\n\n%s", title, message)
	_, err = s.ChannelMessageSend(channelID, testMessage)
	if err != nil {
		respondError(s, i, "Failed to send test message. Check channel ID and bot permissions: "+err.Error())
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ Test message sent for schedule **%s** in <#%s>!", title, channelID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	log.Printf("🧪 TEST SCHEDULE #%d (%s) sent by @%s", scheduleID, title, i.Member.User.Username)
}

func respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "❌ " + message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func scheduleRunner(s *discordgo.Session) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		rows, err := db.Query("SELECT id, message, channel_id, is_repeating, repeat_type, repeat_value, next_run FROM schedules WHERE next_run <= ?", time.Now().UTC())
		if err != nil {
			log.Println("Error querying schedules:", err)
			continue
		}

		for rows.Next() {
			var id int
			var message, channelID, repeatType, repeatValue string
			var isRepeating bool
			var nextRun time.Time

			rows.Scan(&id, &message, &channelID, &isRepeating, &repeatType, &repeatValue, &nextRun)

			_, err := s.ChannelMessageSend(channelID, message)
			if err != nil {
				log.Printf("❌ Error sending message for schedule #%d: %v", id, err)
			} else {
				log.Printf("📤 Sent scheduled message for schedule #%d to channel %s", id, channelID)
			}

			if isRepeating {
				newNextRun := calculateNextRun(nextRun, repeatType, repeatValue)
				db.Exec("UPDATE schedules SET next_run = ? WHERE id = ?", newNextRun, id)
				log.Printf("🔄 Schedule #%d rescheduled for %s", id, newNextRun.Format("2006-01-02 15:04:05"))
			} else {
				db.Exec("DELETE FROM schedules WHERE id = ?", id)
				log.Printf("✅ One-time schedule #%d completed and removed", id)
			}
		}
		rows.Close()
	}
}

func calculateNextRun(lastRun time.Time, repeatType, repeatValue string) time.Time {
	if repeatType == "interval" {
		minutes, _ := strconv.Atoi(repeatValue)
		return lastRun.Add(time.Duration(minutes) * time.Minute)
	} else if repeatType == "days" {
		days := strings.Split(repeatValue, ",")
		dayMap := map[string]time.Weekday{
			"Sun": time.Sunday, "Mon": time.Monday, "Tue": time.Tuesday,
			"Wed": time.Wednesday, "Thu": time.Thursday, "Fri": time.Friday, "Sat": time.Saturday,
		}

		next := lastRun.Add(24 * time.Hour)
		for i := 0; i < 7; i++ {
			for _, day := range days {
				if dayMap[strings.TrimSpace(day)] == next.Weekday() {
					return next
				}
			}
			next = next.Add(24 * time.Hour)
		}
	}
	return lastRun.Add(24 * time.Hour)
}