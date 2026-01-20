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
	"github.com/robfig/cron/v3"
)

var (
	db          *sql.DB
	cronManager *cron.Cron
	admins      []string
	debug       bool
	botSession  *discordgo.Session
	cronJobs    = make(map[int]cron.EntryID)
	containerTZ *time.Location
)

type Schedule struct {
	ID          int
	UserID      string
	Title       string
	Message     string
	ChannelID   string
	RepeatType  string
	RepeatValue string
	Active      bool
	Timezone    string
}

func main() {
	// Try to load .env file, but don't fail if it doesn't exist
	err := godotenv.Load()
	if err != nil {
		log.Println("Info: No .env file found, using environment variables")
	}

	// Get container timezone
	containerTZ = getContainerTimezone()
	log.Printf("Container timezone: %v", containerTZ)

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN not set")
	}

	adminIDs := os.Getenv("ADMIN_IDS")
	if adminIDs == "" {
		log.Fatal("ADMIN_IDS not set")
	}
	admins = strings.Split(adminIDs, ",")
	for i := range admins {
		admins[i] = strings.TrimSpace(admins[i])
	}

	debug = os.Getenv("DEBUG") == "true"

	initDB()
	defer db.Close()

	cronManager = cron.New(cron.WithLocation(containerTZ))
	cronManager.Start()
	defer cronManager.Stop()

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}

	botSession = dg

	dg.AddHandler(ready)
	dg.AddHandler(interactionCreate)

	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages

	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection:", err)
	}

	registerCommands(dg)
	loadSchedules()

	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	dg.Close()
}

func getContainerTimezone() *time.Location {
	// Try to get from environment variable
	tzEnv := os.Getenv("TZ")
	if tzEnv == "" {
		tzEnv = "UTC"
	}

	loc, err := time.LoadLocation(tzEnv)
	if err != nil {
		log.Printf("Warning: Failed to load timezone %s, using UTC: %v", tzEnv, err)
		return time.UTC
	}
	return loc
}

func initDB() {
	var err error
	
	// Use persistent path in Docker, fallback to local
	dbPath := "./schedules.db"
	if _, err := os.Stat("/data"); err == nil {
		dbPath = "/data/schedules.db"
	}
	
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal(err)
	}

	createTables := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		timezone TEXT DEFAULT 'Asia/Kolkata'
	);

	CREATE TABLE IF NOT EXISTS schedules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		title TEXT NOT NULL,
		message TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		repeat_type TEXT NOT NULL,
		repeat_value TEXT,
		active BOOLEAN DEFAULT 1,
		timezone TEXT DEFAULT 'Asia/Kolkata'
	);`

	_, err = db.Exec(createTables)
	if err != nil {
		log.Fatal(err)
	}

	debugLog("Database initialized at: " + dbPath)
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	s.UpdateGameStatus(0, "Scheduling messages")
	debugLog(fmt.Sprintf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator))
	debugLog(fmt.Sprintf("Container timezone: %v", containerTZ))
}

func registerCommands(s *discordgo.Session) {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "help",
			Description: "Show all available commands",
		},
		{
			Name:        "set_timezone",
			Description: "Set your timezone (e.g., Asia/Kolkata)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "timezone",
					Description: "Timezone in IANA format",
					Required:    true,
				},
			},
		},
		{
			Name:        "create_schedule",
			Description: "Create a new message schedule",
		},
		{
			Name:        "list_schedules",
			Description: "List your schedules",
		},
		{
			Name:        "edit_schedule",
			Description: "Edit an existing schedule",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "pause_schedule",
			Description: "Pause a schedule",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "resume_schedule",
			Description: "Resume a paused schedule",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "delete_schedule",
			Description: "Delete a schedule",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "test_schedule",
			Description: "Send a test message immediately",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "admin_list_all",
			Description: "[Admin] List all schedules",
		},
		{
			Name:        "admin_pause",
			Description: "[Admin] Pause any schedule",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "admin_delete",
			Description: "[Admin] Delete any schedule",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Schedule ID",
					Required:    true,
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", cmd)
		if err != nil {
			log.Printf("Cannot create '%v' command: %v", cmd.Name, err)
		}
	}

	debugLog("Commands registered")
}

func interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		handleCommand(s, i)
	case discordgo.InteractionModalSubmit:
		handleModalSubmit(s, i)
	}
}

func handleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	debugLog(fmt.Sprintf("Command '%s' used by %s", i.ApplicationCommandData().Name, i.Member.User.ID))

	switch i.ApplicationCommandData().Name {
	case "help":
		handleHelp(s, i)
	case "set_timezone":
		handleSetTimezone(s, i)
	case "create_schedule":
		handleCreateSchedule(s, i)
	case "list_schedules":
		handleListSchedules(s, i)
	case "edit_schedule":
		handleEditSchedule(s, i)
	case "pause_schedule":
		handlePauseSchedule(s, i)
	case "resume_schedule":
		handleResumeSchedule(s, i)
	case "delete_schedule":
		handleDeleteSchedule(s, i)
	case "test_schedule":
		handleTestSchedule(s, i)
	case "admin_list_all":
		handleAdminListAll(s, i)
	case "admin_pause":
		handleAdminPause(s, i)
	case "admin_delete":
		handleAdminDelete(s, i)
	}
}

func handleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	if data.CustomID == "create_schedule_modal" {
		handleCreateScheduleModal(s, i, data)
	} else if strings.HasPrefix(data.CustomID, "edit_schedule_modal_") {
		handleEditScheduleModal(s, i, data)
	}
}

func handleCreateScheduleModal(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ModalSubmitInteractionData) {
	title := data.Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	message := data.Components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	channelID := data.Components[2].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	repeatType := strings.ToLower(data.Components[3].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value)
	repeatValue := data.Components[4].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value

	if repeatType != "none" && repeatType != "interval" && repeatType != "weekly" {
		respondEphemeral(s, i, "Invalid repeat type. Use: none, interval, or weekly")
		return
	}

	timezone := getUserTimezone(i.Member.User.ID)

	result, err := db.Exec("INSERT INTO schedules (user_id, title, message, channel_id, repeat_type, repeat_value, timezone) VALUES (?, ?, ?, ?, ?, ?, ?)",
		i.Member.User.ID, title, message, channelID, repeatType, repeatValue, timezone)
	if err != nil {
		respondEphemeral(s, i, "Error creating schedule: "+err.Error())
		return
	}

	scheduleID, _ := result.LastInsertId()

	scheduleJob(int(scheduleID), channelID, message, repeatType, repeatValue, timezone)

	debugLog(fmt.Sprintf("User %s created schedule %d: %s", i.Member.User.ID, scheduleID, title))
	respondEphemeral(s, i, fmt.Sprintf("✅ Schedule created! ID: %d\nTitle: %s\nType: %s", scheduleID, title, repeatType))
}

func handleEditScheduleModal(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ModalSubmitInteractionData) {
	scheduleIDStr := strings.TrimPrefix(data.CustomID, "edit_schedule_modal_")
	scheduleID, _ := strconv.Atoi(scheduleIDStr)

	title := data.Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	message := data.Components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	channelID := data.Components[2].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
	repeatType := strings.ToLower(data.Components[3].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value)
	repeatValue := data.Components[4].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value

	if repeatType != "none" && repeatType != "interval" && repeatType != "weekly" {
		respondEphemeral(s, i, "Invalid repeat type. Use: none, interval, or weekly")
		return
	}

	timezone := getUserTimezone(i.Member.User.ID)

	_, err := db.Exec("UPDATE schedules SET title = ?, message = ?, channel_id = ?, repeat_type = ?, repeat_value = ?, timezone = ? WHERE id = ? AND user_id = ?",
		title, message, channelID, repeatType, repeatValue, timezone, scheduleID, i.Member.User.ID)
	if err != nil {
		respondEphemeral(s, i, "Error updating schedule")
		return
	}

	removeScheduleJob(scheduleID)
	scheduleJob(scheduleID, channelID, message, repeatType, repeatValue, timezone)

	debugLog(fmt.Sprintf("User %s edited schedule %d", i.Member.User.ID, scheduleID))
	respondEphemeral(s, i, fmt.Sprintf("✅ Schedule %d updated!", scheduleID))
}

func handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	helpText := `**Message Scheduler Bot Commands**

**User Commands:**
/set_timezone - Set your timezone (e.g., Asia/Kolkata)
/create_schedule - Create a new message schedule
/list_schedules - List your schedules
/edit_schedule - Edit an existing schedule
/pause_schedule - Pause a schedule
/resume_schedule - Resume a paused schedule
/delete_schedule - Delete a schedule
/test_schedule - Test a schedule by sending immediately

**Admin Commands:**
/admin_list_all - List all schedules from all users
/admin_pause - Pause any user's schedule
/admin_delete - Delete any user's schedule

**Repeat Types:**
**none** - Send once (leave repeat_value empty or specify time: 2024-12-25 10:00)
**interval** - Repeat every X time (examples: 30m, 2h, 1h30m)
**weekly** - Repeat on specific days (examples: Mon,Wed,Fri 09:00 or Tue,Thu 14:30)

**Days:** Mon, Tue, Wed, Thu, Fri, Sat, Sun
**Time format:** 24-hour (e.g., 09:00, 14:30, 23:45)`

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

	_, err := time.LoadLocation(timezone)
	if err != nil {
		respondEphemeral(s, i, "Invalid timezone format. Use IANA timezone format (e.g., Asia/Kolkata)")
		return
	}

	_, err = db.Exec("INSERT OR REPLACE INTO users (id, timezone) VALUES (?, ?)", i.Member.User.ID, timezone)
	if err != nil {
		respondEphemeral(s, i, "Error saving timezone")
		return
	}

	debugLog(fmt.Sprintf("User %s set timezone to %s", i.Member.User.ID, timezone))
	respondEphemeral(s, i, fmt.Sprintf("✅ Timezone set to %s", timezone))
}

func handleCreateSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "create_schedule_modal",
			Title:    "Create New Schedule",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "title",
							Label:       "Schedule Title",
							Style:       discordgo.TextInputShort,
							Placeholder: "My Daily Reminder",
							Required:    true,
							MaxLength:   100,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "message",
							Label:       "Message to Send",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "Hello everyone!",
							Required:    true,
							MaxLength:   2000,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "channel",
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
							CustomID:    "repeat_type",
							Label:       "Repeat Type (none/interval/weekly)",
							Style:       discordgo.TextInputShort,
							Placeholder: "none",
							Required:    true,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "repeat_value",
							Label:       "Repeat Config (see /help)",
							Style:       discordgo.TextInputShort,
							Placeholder: "60m OR Mon,Wed,Fri 09:00",
							Required:    false,
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

func handleListSchedules(s *discordgo.Session, i *discordgo.InteractionCreate) {
	rows, err := db.Query("SELECT id, title, channel_id, repeat_type, active FROM schedules WHERE user_id = ?", i.Member.User.ID)
	if err != nil {
		respondEphemeral(s, i, "Error fetching schedules")
		return
	}
	defer rows.Close()

	var schedules []string
	for rows.Next() {
		var id int
		var title, channelID, repeatType string
		var active bool
		rows.Scan(&id, &title, &channelID, &repeatType, &active)

		status := "✅ Active"
		if !active {
			status = "⏸️ Paused"
		}

		schedules = append(schedules, fmt.Sprintf("**ID %d**: %s | %s | Type: %s | Channel: <#%s>", id, title, status, repeatType, channelID))
	}

	if len(schedules) == 0 {
		respondEphemeral(s, i, "You have no schedules. Use /create_schedule to create one!")
		return
	}

	respondEphemeral(s, i, "**Your Schedules:**\n"+strings.Join(schedules, "\n"))
}

func handlePauseSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())

	result, err := db.Exec("UPDATE schedules SET active = 0 WHERE id = ? AND user_id = ?", id, i.Member.User.ID)
	if err != nil {
		respondEphemeral(s, i, "Error pausing schedule")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondEphemeral(s, i, "Schedule not found or you don't have permission")
		return
	}

	removeScheduleJob(id)

	debugLog(fmt.Sprintf("User %s paused schedule %d", i.Member.User.ID, id))
	respondEphemeral(s, i, fmt.Sprintf("⏸️ Schedule %d paused", id))
}

func handleResumeSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())

	var channelID, message, repeatType, repeatValue, timezone string
	err := db.QueryRow("SELECT channel_id, message, repeat_type, repeat_value, timezone FROM schedules WHERE id = ? AND user_id = ?",
		id, i.Member.User.ID).Scan(&channelID, &message, &repeatType, &repeatValue, &timezone)

	if err != nil {
		respondEphemeral(s, i, "Schedule not found or you don't have permission")
		return
	}

	_, err = db.Exec("UPDATE schedules SET active = 1 WHERE id = ?", id)
	if err != nil {
		respondEphemeral(s, i, "Error resuming schedule")
		return
	}

	scheduleJob(id, channelID, message, repeatType, repeatValue, timezone)

	debugLog(fmt.Sprintf("User %s resumed schedule %d", i.Member.User.ID, id))
	respondEphemeral(s, i, fmt.Sprintf("▶️ Schedule %d resumed", id))
}

func handleDeleteSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())

	result, err := db.Exec("DELETE FROM schedules WHERE id = ? AND user_id = ?", id, i.Member.User.ID)
	if err != nil {
		respondEphemeral(s, i, "Error deleting schedule")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondEphemeral(s, i, "Schedule not found or you don't have permission")
		return
	}

	removeScheduleJob(id)

	debugLog(fmt.Sprintf("User %s deleted schedule %d", i.Member.User.ID, id))
	respondEphemeral(s, i, fmt.Sprintf("🗑️ Schedule %d deleted", id))
}

func handleTestSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())

	var message, channelID string
	err := db.QueryRow("SELECT message, channel_id FROM schedules WHERE id = ? AND user_id = ?", id, i.Member.User.ID).Scan(&message, &channelID)
	if err != nil {
		respondEphemeral(s, i, "Schedule not found or you don't have permission")
		return
	}

	_, err = s.ChannelMessageSend(channelID, message)
	if err != nil {
		respondEphemeral(s, i, "Error sending test message. Check channel permissions and ID.")
		return
	}

	debugLog(fmt.Sprintf("User %s tested schedule %d", i.Member.User.ID, id))
	respondEphemeral(s, i, "✅ Test message sent!")
}

func handleEditSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := int(i.ApplicationCommandData().Options[0].IntValue())

	var title, message, channelID, repeatType, repeatValue string
	err := db.QueryRow("SELECT title, message, channel_id, repeat_type, repeat_value FROM schedules WHERE id = ? AND user_id = ?",
		id, i.Member.User.ID).Scan(&title, &message, &channelID, &repeatType, &repeatValue)

	if err != nil {
		respondEphemeral(s, i, "Schedule not found or you don't have permission")
		return
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: fmt.Sprintf("edit_schedule_modal_%d", id),
			Title:    "Edit Schedule",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "title",
							Label:       "Schedule Title",
							Style:       discordgo.TextInputShort,
							Value:       title,
							Required:    true,
							MaxLength:   100,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "message",
							Label:       "Message to Send",
							Style:       discordgo.TextInputParagraph,
							Value:       message,
							Required:    true,
							MaxLength:   2000,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "channel",
							Label:       "Channel ID",
							Style:       discordgo.TextInputShort,
							Value:       channelID,
							Required:    true,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "repeat_type",
							Label:       "Repeat Type",
							Style:       discordgo.TextInputShort,
							Value:       repeatType,
							Required:    true,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "repeat_value",
							Label:       "Repeat Config",
							Style:       discordgo.TextInputShort,
							Value:       repeatValue,
							Required:    false,
						},
					},
				},
			},
		},
	})

	if err != nil {
		log.Println("Error showing edit modal:", err)
	}
}

func handleAdminListAll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(i.Member.User.ID) {
		respondEphemeral(s, i, "❌ You don't have permission to use this command")
		return
	}

	rows, err := db.Query("SELECT id, user_id, title, channel_id, repeat_type, active FROM schedules")
	if err != nil {
		respondEphemeral(s, i, "Error fetching schedules")
		return
	}
	defer rows.Close()

	var schedules []string
	for rows.Next() {
		var id int
		var userID, title, channelID, repeatType string
		var active bool
		rows.Scan(&id, &userID, &title, &channelID, &repeatType, &active)

		status := "✅"
		if !active {
			status = "⏸️"
		}

		schedules = append(schedules, fmt.Sprintf("%s **ID %d**: %s | User: <@%s> | Type: %s", status, id, title, userID, repeatType))
	}

	if len(schedules) == 0 {
		respondEphemeral(s, i, "No schedules found")
		return
	}

	debugLog(fmt.Sprintf("Admin %s listed all schedules", i.Member.User.ID))
	respondEphemeral(s, i, "**All Schedules:**\n"+strings.Join(schedules, "\n"))
}

func handleAdminPause(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(i.Member.User.ID) {
		respondEphemeral(s, i, "❌ You don't have permission to use this command")
		return
	}

	id := int(i.ApplicationCommandData().Options[0].IntValue())

	_, err := db.Exec("UPDATE schedules SET active = 0 WHERE id = ?", id)
	if err != nil {
		respondEphemeral(s, i, "Error pausing schedule")
		return
	}

	removeScheduleJob(id)

	debugLog(fmt.Sprintf("Admin %s paused schedule %d", i.Member.User.ID, id))
	respondEphemeral(s, i, fmt.Sprintf("⏸️ Schedule %d paused", id))
}

func handleAdminDelete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(i.Member.User.ID) {
		respondEphemeral(s, i, "❌ You don't have permission to use this command")
		return
	}

	id := int(i.ApplicationCommandData().Options[0].IntValue())

	_, err := db.Exec("DELETE FROM schedules WHERE id = ?", id)
	if err != nil {
		respondEphemeral(s, i, "Error deleting schedule")
		return
	}

	removeScheduleJob(id)

	debugLog(fmt.Sprintf("Admin %s deleted schedule %d", i.Member.User.ID, id))
	respondEphemeral(s, i, fmt.Sprintf("🗑️ Schedule %d deleted", id))
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func getUserTimezone(userID string) string {
	var timezone string
	err := db.QueryRow("SELECT timezone FROM users WHERE id = ?", userID).Scan(&timezone)
	if err != nil {
		return "Asia/Kolkata"
	}
	return timezone
}

func isAdmin(userID string) bool {
	for _, admin := range admins {
		if admin == userID {
			return true
		}
	}
	return false
}

func debugLog(message string) {
	if debug {
		log.Println("[DEBUG]", message)
	}
}

func loadSchedules() {
	rows, err := db.Query("SELECT id, channel_id, message, repeat_type, repeat_value, timezone FROM schedules WHERE active = 1")
	if err != nil {
		log.Println("Error loading schedules:", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var channelID, message, repeatType, repeatValue, timezone string
		rows.Scan(&id, &channelID, &message, &repeatType, &repeatValue, &timezone)

		scheduleJob(id, channelID, message, repeatType, repeatValue, timezone)
		count++
	}

	debugLog(fmt.Sprintf("Loaded %d active schedules", count))
}

func scheduleJob(id int, channelID, message, repeatType, repeatValue, timezone string) {
	// Get user's timezone
	userLoc, err := time.LoadLocation(timezone)
	if err != nil {
		debugLog(fmt.Sprintf("Schedule %d: Invalid timezone %s, using UTC", id, timezone))
		userLoc = time.UTC
	}

	var cronSpec string

	switch repeatType {
	case "interval":
		// Parse interval like "30m", "2h", "1h30m"
		duration, err := time.ParseDuration(repeatValue)
		if err != nil {
			log.Printf("Invalid interval for schedule %d: %s", id, repeatValue)
			return
		}

		// Use cron's @every syntax (always in container timezone)
		cronSpec = fmt.Sprintf("@every %s", duration.String())
		debugLog(fmt.Sprintf("Schedule %d: Interval %s -> cron: %s", id, repeatValue, cronSpec))

	case "weekly":
		// Parse weekly schedule like "Mon,Wed,Fri 09:00"
		parts := strings.Split(repeatValue, " ")
		if len(parts) != 2 {
			log.Printf("Invalid weekly format for schedule %d: %s", id, repeatValue)
			return
		}

		daysStr := parts[0]
		timeStr := parts[1]

		timeParts := strings.Split(timeStr, ":")
		if len(timeParts) != 2 {
			log.Printf("Invalid time format for schedule %d: %s", id, timeStr)
			return
		}

		userHour, err := strconv.Atoi(timeParts[0])
		if err != nil {
			log.Printf("Invalid hour for schedule %d: %s", id, timeParts[0])
			return
		}

		userMinute, err := strconv.Atoi(timeParts[1])
		if err != nil {
			log.Printf("Invalid minute for schedule %d: %s", id, timeParts[1])
			return
		}

		// Parse days
		days := strings.Split(daysStr, ",")
		dayMap := map[string]string{
			"sun": "0", "mon": "1", "tue": "2", "wed": "3",
			"thu": "4", "fri": "5", "sat": "6",
		}

		// Create a time in user's timezone to convert to container timezone
		// We'll use the next occurrence of each day for calculation
		now := time.Now().In(userLoc)
		containerDays := make(map[int]bool) // Track unique container days

		// For each day specified, find the next occurrence and convert to container timezone
		for _, day := range days {
			dayLower := strings.ToLower(strings.TrimSpace(day))
			if userDayNumStr, ok := dayMap[dayLower]; ok {
				userDayNum, _ := strconv.Atoi(userDayNumStr)

				// Calculate the next occurrence of this day at the specified time in user's timezone
				daysUntilNext := (userDayNum - int(now.Weekday()) + 7) % 7
				if daysUntilNext == 0 {
					// If it's today, check if the time has passed
					userTimeToday := time.Date(now.Year(), now.Month(), now.Day(), userHour, userMinute, 0, 0, userLoc)
					if userTimeToday.Before(now) {
						daysUntilNext = 7 // Move to next week
					}
				}

				targetDate := now.AddDate(0, 0, daysUntilNext)
				userTime := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), userHour, userMinute, 0, 0, userLoc)

				// Convert to container timezone
				containerTime := userTime.In(containerTZ)

				// Store the container day and time
				containerDays[int(containerTime.Weekday())] = true

				debugLog(fmt.Sprintf("Schedule %d: User %s %s %02d:%02d -> Container %s %02d:%02d",
					id, day, timezone, userHour, userMinute,
					containerTime.Weekday().String(), containerTime.Hour(), containerTime.Minute()))
			}
		}

		if len(containerDays) == 0 {
			log.Printf("No valid days for schedule %d", id)
			return
		}

		// For simplicity, we'll use the time from the first day's conversion
		// In practice, all should have the same hour/minute conversion
		// Pick a reference day to get the time
		firstUserDay := strings.ToLower(strings.TrimSpace(days[0]))
		if userDayNumStr, ok := dayMap[firstUserDay]; ok {
			userDayNum, _ := strconv.Atoi(userDayNumStr)
			daysUntilNext := (userDayNum - int(now.Weekday()) + 7) % 7
			if daysUntilNext == 0 {
				userTimeToday := time.Date(now.Year(), now.Month(), now.Day(), userHour, userMinute, 0, 0, userLoc)
				if userTimeToday.Before(now) {
					daysUntilNext = 7
				}
			}

			targetDate := now.AddDate(0, 0, daysUntilNext)
			userTime := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), userHour, userMinute, 0, 0, userLoc)
			containerTime := userTime.In(containerTZ)

			// Build list of container day numbers
			var containerDayNumbers []string
			for dayNum := range containerDays {
				containerDayNumbers = append(containerDayNumbers, strconv.Itoa(dayNum))
			}

			// Cron format: minute hour * * day (in container timezone)
			cronSpec = fmt.Sprintf("%d %d * * %s",
				containerTime.Minute(),
				containerTime.Hour(),
				strings.Join(containerDayNumbers, ","))

			debugLog(fmt.Sprintf("Schedule %d: Final cron spec: %s (Container TZ: %v)",
				id, cronSpec, containerTZ))
		}

	case "none":
		// One-time schedule
		if repeatValue == "" {
			// Execute immediately
			go sendScheduledMessage(id, channelID, message)
			return
		}

		// Parse specific time in user's timezone
		userTime, err := time.ParseInLocation("2006-01-02 15:04", repeatValue, userLoc)
		if err != nil {
			log.Printf("Invalid time format for schedule %d: %s", id, repeatValue)
			return
		}

		// Convert to container timezone
		containerTime := userTime.In(containerTZ)
		duration := time.Until(containerTime)

		if duration < 0 {
			log.Printf("Schedule %d time is in the past: %s", id, repeatValue)
			return
		}

		debugLog(fmt.Sprintf("Schedule %d: One-time at %s (%s) -> %s (%s), duration: %v",
			id, userTime.Format("2006-01-02 15:04"), timezone,
			containerTime.Format("2006-01-02 15:04"), containerTZ, duration))

		time.AfterFunc(duration, func() {
			sendScheduledMessage(id, channelID, message)
			// Disable after sending
			db.Exec("UPDATE schedules SET active = 0 WHERE id = ?", id)
			debugLog(fmt.Sprintf("One-time schedule %d completed and disabled", id))
		})

		return

	default:
		log.Printf("Unknown repeat type for schedule %d: %s", id, repeatType)
		return
	}

	// Add cron job with container timezone
	entryID, err := cronManager.AddFunc(cronSpec, func() {
		sendScheduledMessage(id, channelID, message)
	})

	if err != nil {
		log.Printf("Error scheduling job %d: %v", id, err)
		return
	}

	cronJobs[id] = entryID
	debugLog(fmt.Sprintf("Scheduled job %d with spec: %s", id, cronSpec))
}

func sendScheduledMessage(scheduleID int, channelID, message string) {
	// Check if schedule is still active
	var active bool
	var title, userTimezone string
	err := db.QueryRow("SELECT active, title, timezone FROM schedules WHERE id = ?", scheduleID).Scan(&active, &title, &userTimezone)
	if err != nil || !active {
		debugLog(fmt.Sprintf("Schedule %d is inactive or not found, skipping message", scheduleID))
		return
	}

	debugLog(fmt.Sprintf("Attempting to send schedule %d ('%s') to channel %s", scheduleID, title, channelID))
	debugLog(fmt.Sprintf("Message content (first 100 chars): %.100s", message))

	// Try to send message
	msg, err := botSession.ChannelMessageSend(channelID, message)
	if err != nil {
		log.Printf("ERROR sending scheduled message for schedule %d: %v", scheduleID, err)
		
		// Try to get channel info for debugging
		channel, channelErr := botSession.Channel(channelID)
		if channelErr != nil {
			log.Printf("ERROR: Could not fetch channel %s: %v", channelID, channelErr)
		} else {
			log.Printf("Channel info: Name=%s, Guild=%s, Type=%d", channel.Name, channel.GuildID, channel.Type)
		}
	} else {
		debugLog(fmt.Sprintf("SUCCESS: Sent scheduled message for schedule %d to channel %s (Message ID: %s)", 
			scheduleID, channelID, msg.ID))
	}
}

func removeScheduleJob(scheduleID int) {
	if entryID, exists := cronJobs[scheduleID]; exists {
		cronManager.Remove(entryID)
		delete(cronJobs, scheduleID)
		debugLog(fmt.Sprintf("Removed cron job for schedule %d", scheduleID))
	}
}