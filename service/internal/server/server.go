package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/berjistech/berjis-ecosystem/schools/service/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type Options struct {
	AllowedOrigins string
	CoreAPIBase    string
	DB             *sqlx.DB
}

func New(opts Options) *fiber.App {
	app := fiber.New()
	ao := strings.TrimSpace(opts.AllowedOrigins)
	allowCreds := true
	if ao == "" || ao == "*" || strings.Contains(ao, "*") {
		allowCreds = false
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins:     ao,
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Authorization,Content-Type,Accept",
		AllowCredentials: allowCreds,
	}))

	app.Get("/v1/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"success": true}) })

	// Local uploads directory and static serving
	uploadDir := strings.TrimSpace(os.Getenv("UPLOAD_DIR"))
	if uploadDir == "" {
		uploadDir = "/tmp/uploads"
	}
	_ = os.MkdirAll(uploadDir, 0o755)
	app.Static("/uploads", uploadDir)

	// Upload feature flags
	uploadEnabled := true
	if v := strings.TrimSpace(os.Getenv("UPLOAD_ENABLED")); v != "" {
		uploadEnabled = strings.EqualFold(v, "true") || v == "1"
	}
	proxyUploadURL := strings.TrimSpace(os.Getenv("FILE_UPLOAD_PROXY_URL"))
	maxBytes := int64(2 * 1024 * 1024) // 2MB default for business docs
	if v := strings.TrimSpace(os.Getenv("UPLOAD_MAX_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}

	// Simple upload endpoint (local or proxy)
	app.Post("/v1/uploads", func(c *fiber.Ctx) error {
		if !uploadEnabled && proxyUploadURL == "" {
			return c.Status(403).JSON(fiber.Map{"success": false, "message": "Uploads are disabled"})
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Missing file"})
		}
		// Type check PDF
		if !strings.EqualFold(filepath.Ext(fh.Filename), ".pdf") {
			if ct := fh.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/pdf") {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Only PDF uploads are allowed"})
			}
		}
		// Size limit
		if fh.Size > 0 && fh.Size > maxBytes {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": fmt.Sprintf("File too large (max %d bytes)", maxBytes)})
		}

		// Proxy mode
		if proxyUploadURL != "" {
			file, err := fh.Open()
			if err != nil {
				return fiber.ErrBadRequest
			}
			defer file.Close()
			// Build multipart request
			var b bytes.Buffer
			w := multipart.NewWriter(&b)
			part, err := w.CreateFormFile("file", fh.Filename)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			if _, err := io.Copy(part, file); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			w.Close()
			req, _ := http.NewRequest("POST", proxyUploadURL, &b)
			req.Header.Set("Content-Type", w.FormDataContentType())
			if v := c.Get("Authorization"); v != "" {
				req.Header.Set("Authorization", v)
			}
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return c.Status(502).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			defer resp.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&out)
			return c.Status(resp.StatusCode).JSON(out)
		}

		// Local save
		day := time.Now().Format("20060102")
		subdir := filepath.Join(uploadDir, day)
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		name := uuid.New().String() + strings.ToLower(filepath.Ext(fh.Filename))
		dest := filepath.Join(subdir, name)
		if err := c.SaveFile(fh, dest); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		urlPath := "/uploads/" + day + "/" + name
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": urlPath, "filename": fh.Filename}})
	})

	// Auth verify against Core API
	getUserID := func(c *fiber.Ctx) (string, error) {
		req, _ := http.NewRequest("GET", strings.TrimRight(opts.CoreAPIBase, "/")+"/v1/auth/verify", nil)
		if v := c.Get("Authorization"); v != "" {
			req.Header.Set("Authorization", v)
		}
		if v := c.Get("Cookie"); v != "" {
			req.Header.Set("Cookie", v)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return "", err
		}
		data, _ := raw["data"].(map[string]any)
		if data == nil {
			return "", fiber.ErrUnauthorized
		}
		if valid, _ := data["valid"].(bool); !valid {
			return "", fiber.ErrUnauthorized
		}
		if uidAny, ok := data["uid"]; ok {
			switch v := uidAny.(type) {
			case float64:
				return fmt.Sprintf("%0.0f", v), nil
			case string:
				return v, nil
			}
		}
		if uidStr, ok := data["userId"].(string); ok && uidStr != "" {
			return uidStr, nil
		}
		return "", fiber.ErrUnauthorized
	}

	// Platform admin checker
	isPlatformAdmin := func(uid string) bool {
		if opts.DB == nil || uid == "" {
			return false
		}
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM platform_admins WHERE user_id=$1)`, uid)
		return ok
	}

	// --- Schools ---
	type school struct {
		ID          string    `json:"id" db:"id"`
		OwnerUserID string    `json:"ownerUserId" db:"owner_user_id"`
		Name        string    `json:"name" db:"name"`
		Description *string   `json:"description,omitempty" db:"description"`
		IsVerified  bool      `json:"isVerified" db:"is_verified"`
		CreatedAt   time.Time `json:"createdAt" db:"created_at"`
		UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
	}
	app.Get("/v1/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		rows := []school{}
		if err := opts.DB.Select(&rows, `SELECT id, owner_user_id, name, description, is_verified, created_at, updated_at FROM schools ORDER BY created_at DESC`); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// List schools where current user is a member (optionally restricted by role)
	app.Get("/v1/schools/mine", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		role := strings.TrimSpace(strings.ToLower(c.Query("role")))
		rows := []school{}
		if role == "" || role == "any" {
			if err := opts.DB.Select(&rows, `SELECT s.id, s.owner_user_id, s.name, s.description, s.is_verified, s.created_at, s.updated_at
                FROM schools s JOIN school_members m ON m.school_id=s.id WHERE m.user_id=$1 AND m.status='active'
                ORDER BY s.created_at DESC`, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT s.id, s.owner_user_id, s.name, s.description, s.is_verified, s.created_at, s.updated_at
                FROM schools s JOIN school_members m ON m.school_id=s.id WHERE m.user_id=$1 AND m.role=$2 AND m.status='active'
                ORDER BY s.created_at DESC`, uid, role); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type schoolIn struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	app.Post("/v1/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in schoolIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Name) == "" {
			return fiber.ErrBadRequest
		}
		var out school
		err = opts.DB.Get(&out, `INSERT INTO schools (owner_user_id, name, description) VALUES ($1,$2,$3)
            RETURNING id, owner_user_id, name, description, is_verified, created_at, updated_at`, uid, in.Name, in.Description)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// owner becomes admin member
		_, _ = opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role) VALUES ($1,$2,'admin') ON CONFLICT DO NOTHING`, out.ID, uid)
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Classes ---
	type class struct {
		ID          string    `json:"id" db:"id"`
		SchoolID    *string   `json:"schoolId,omitempty" db:"school_id"`
		TutorUserID string    `json:"tutorUserId" db:"tutor_user_id"`
		Title       string    `json:"title" db:"title"`
		Description *string   `json:"description,omitempty" db:"description"`
		Visibility  string    `json:"visibility" db:"visibility"`
		IsPaid      bool      `json:"isPaid" db:"is_paid"`
		PriceCents  int       `json:"priceCents" db:"price_cents"`
		CreatedAt   time.Time `json:"createdAt" db:"created_at"`
		UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
	}
	app.Get("/v1/classes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		schoolID := c.Query("school_id")
		uid, _ := getUserID(c) // optional; if present include owned/private
		rows := []class{}
		if schoolID != "" {
			if err := opts.DB.Select(&rows, `SELECT id, school_id, tutor_user_id, title, description, visibility, is_paid, price_cents, created_at, updated_at FROM classes WHERE school_id=$1 OR tutor_user_id=$2 OR visibility='public' ORDER BY created_at DESC`, schoolID, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT id, school_id, tutor_user_id, title, description, visibility, is_paid, price_cents, created_at, updated_at FROM classes WHERE visibility='public' OR tutor_user_id=$1 ORDER BY created_at DESC`, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type classIn struct {
		SchoolID    *string `json:"schoolId"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Visibility  string  `json:"visibility"`
		IsPaid      *bool   `json:"isPaid"`
		PriceCents  *int    `json:"priceCents"`
	}
	app.Post("/v1/classes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in classIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title) == "" {
			return fiber.ErrBadRequest
		}
		vis := in.Visibility
		if vis == "" {
			vis = "school"
		}
		isPaid := false
		if in.IsPaid != nil {
			isPaid = *in.IsPaid
		}
		price := 0
		if in.PriceCents != nil {
			price = *in.PriceCents
		}
		if in.SchoolID != nil && *in.SchoolID != "" {
			okA, _ := auth.IsSchoolAdmin(opts.DB, *in.SchoolID, uid)
			okT, _ := auth.IsSchoolTutor(opts.DB, *in.SchoolID, uid)
			if !okA && !okT {
				return fiber.ErrForbidden
			}
		}
		var out class
		err = opts.DB.Get(&out, `INSERT INTO classes (school_id, tutor_user_id, title, description, visibility, is_paid, price_cents)
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, tutor_user_id, title, description, visibility, is_paid, price_cents, created_at, updated_at`,
			in.SchoolID, uid, in.Title, in.Description, vis, isPaid, price)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	app.Post("/v1/classes/:id/enroll", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		if _, err := opts.DB.Exec(`INSERT INTO class_enrollments (class_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Subjects ---
	type subject struct {
		ID          string  `json:"id" db:"id"`
		ClassID     string  `json:"classId" db:"class_id"`
		Title       string  `json:"title" db:"title"`
		Description *string `json:"description,omitempty" db:"description"`
		OrderIndex  int     `json:"orderIndex" db:"order_index"`
	}
	app.Get("/v1/subjects", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		classID := c.Query("class_id")
		rows := []subject{}
		if classID == "" {
			return fiber.ErrBadRequest
		}
		if err := opts.DB.Select(&rows, `SELECT id, class_id, title, description, order_index FROM subjects WHERE class_id=$1 ORDER BY order_index ASC, title ASC`, classID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type subjectIn struct {
		ClassID     string  `json:"classId"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		OrderIndex  *int    `json:"orderIndex"`
	}
	app.Post("/v1/subjects", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		_ = uid
		var in subjectIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.ClassID) == "" {
			return fiber.ErrBadRequest
		}
		okCT, _ := auth.IsClassTutor(opts.DB, in.ClassID, uid)
		if !okCT {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, in.ClassID)
			if sid != nil && *sid != "" {
				okA, _ := auth.IsSchoolAdmin(opts.DB, *sid, uid)
				if !okA {
					return fiber.ErrForbidden
				}
			} else {
				return fiber.ErrForbidden
			}
		}
		ord := 0
		if in.OrderIndex != nil {
			ord = *in.OrderIndex
		}
		var out subject
		if err := opts.DB.Get(&out, `INSERT INTO subjects (class_id, title, description, order_index) VALUES ($1,$2,$3,$4) RETURNING id, class_id, title, description, order_index`, in.ClassID, in.Title, in.Description, ord); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Lessons ---
	type lesson struct {
		ID         string          `json:"id" db:"id"`
		SubjectID  string          `json:"subjectId" db:"subject_id"`
		Title      string          `json:"title" db:"title"`
		Type       string          `json:"type" db:"type"`
		Content    json.RawMessage `json:"content,omitempty" db:"content"`
		OrderIndex int             `json:"orderIndex" db:"order_index"`
		IsFree     bool            `json:"isFree" db:"is_free"`
	}
	app.Get("/v1/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Query("subject_id")
		if sid == "" {
			return fiber.ErrBadRequest
		}
		rows := []lesson{}
		if err := opts.DB.Select(&rows, `SELECT id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free FROM lessons WHERE subject_id=$1 ORDER BY order_index ASC, title ASC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type lessonIn struct {
		SubjectID  string          `json:"subjectId"`
		Title      string          `json:"title"`
		Type       string          `json:"type"`
		Content    json.RawMessage `json:"content"`
		OrderIndex *int            `json:"orderIndex"`
		IsFree     *bool           `json:"isFree"`
	}
	app.Post("/v1/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in lessonIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.SubjectID) == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Type) == "" {
			return fiber.ErrBadRequest
		}
		var classID string
		if err := opts.DB.Get(&classID, `SELECT class_id FROM subjects WHERE id=$1`, in.SubjectID); err != nil {
			return fiber.ErrBadRequest
		}
		okCT, _ := auth.IsClassTutor(opts.DB, classID, uid)
		var okA bool
		if !okCT {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
			if sid != nil && *sid != "" {
				okA, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !okCT && !okA {
			return fiber.ErrForbidden
		}
		ord := 0
		if in.OrderIndex != nil {
			ord = *in.OrderIndex
		}
		free := false
		if in.IsFree != nil {
			free = *in.IsFree
		}
		var out lesson
		if err := opts.DB.Get(&out, `INSERT INTO lessons (subject_id, title, type, content, order_index, is_free) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free`, in.SubjectID, in.Title, in.Type, nullIfEmptyJSON(in.Content), ord, free); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Tests ---
	type test struct {
		ID          string    `json:"id" db:"id"`
		SchoolID    *string   `json:"schoolId,omitempty" db:"school_id"`
		SubjectID   *string   `json:"subjectId,omitempty" db:"subject_id"`
		LessonID    *string   `json:"lessonId,omitempty" db:"lesson_id"`
		Title       string    `json:"title" db:"title"`
		Description *string   `json:"description,omitempty" db:"description"`
		Visibility  string    `json:"visibility" db:"visibility"`
		CreatedBy   string    `json:"createdByUserId" db:"created_by_user_id"`
		CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	}
	type testQuestion struct {
		ID         string          `json:"id" db:"id"`
		TestID     string          `json:"testId" db:"test_id"`
		QType      string          `json:"qtype" db:"qtype"`
		Prompt     string          `json:"prompt" db:"prompt"`
		Options    json.RawMessage `json:"options,omitempty" db:"options"`
		Answer     json.RawMessage `json:"answer,omitempty" db:"answer"`
		Points     int             `json:"points" db:"points"`
		OrderIndex int             `json:"orderIndex" db:"order_index"`
	}
	app.Get("/v1/tests", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		sid := c.Query("subject_id")
		lid := c.Query("lesson_id")
		rows := []test{}
		var err error
		if lid != "" {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests WHERE lesson_id=$1 ORDER BY created_at DESC`, lid)
		} else if sid != "" {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests WHERE subject_id=$1 ORDER BY created_at DESC`, sid)
		} else {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests ORDER BY created_at DESC`)
		}
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	type testIn struct {
		SchoolID    *string `json:"schoolId"`
		SubjectID   *string `json:"subjectId"`
		LessonID    *string `json:"lessonId"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Visibility  string  `json:"visibility"`
	}
	app.Post("/v1/tests", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var in testIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title) == "" {
			return fiber.ErrBadRequest
		}
		vis := in.Visibility
		if vis == "" {
			vis = "private"
		}
		var out test
		if err := opts.DB.Get(&out, `INSERT INTO tests (school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id)
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at`, in.SchoolID, in.SubjectID, in.LessonID, in.Title, in.Description, vis, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// Get a specific test with questions (if permitted)
	app.Get("/v1/tests/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var t test
		if err := opts.DB.Get(&t, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		// Permission check similar to search visibility for tests; also resolve classId and canEdit
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
		// If public, allow; else require tutor/enrolled/member
		canEdit := false
		if t.Visibility != "public" {
			var allow bool
			if classID != nil && *classID != "" {
				// class tutor or enrolled
				var tutorID string
				_ = opts.DB.Get(&tutorID, `SELECT tutor_user_id FROM classes WHERE id=$1`, classID)
				if tutorID == uid {
					allow = true
				}
				if tutorID == uid {
					canEdit = true
				}
				if !allow {
					var exists bool
					_ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2)`, classID, uid)
					if exists {
						allow = true
					}
				}
				if !allow {
					var sid *string
					_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
					if sid != nil && *sid != "" {
						allow, _ = auth.IsSchoolMember(opts.DB, *sid, uid)
						if !canEdit {
							canEdit, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
						}
					}
				}
			}
			if !allow {
				return fiber.ErrForbidden
			}
		}
		// Load questions
		qs := []testQuestion{}
		if err := opts.DB.Select(&qs, `SELECT id, test_id, qtype, prompt, COALESCE(options,'null'::jsonb) AS options, COALESCE(answer,'null'::jsonb) AS answer, points, order_index FROM test_questions WHERE test_id=$1 ORDER BY order_index, id`, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"test": t, "questions": qs, "classId": classID, "canEdit": canEdit}})
	})

	// Create a question under a test (tutor/school admin only)
	type questionIn struct {
		QType      string          `json:"qtype"`
		Prompt     string          `json:"prompt"`
		Options    json.RawMessage `json:"options"`
		Answer     json.RawMessage `json:"answer"`
		Points     *int            `json:"points"`
		OrderIndex *int            `json:"orderIndex"`
	}
	app.Post("/v1/tests/:id/questions", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		// Resolve related class for permission checks
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
		var allow bool
		if classID != nil && *classID != "" {
			allow, _ = auth.IsClassTutor(opts.DB, *classID, uid)
			if !allow {
				var sid *string
				_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
				if sid != nil && *sid != "" {
					allow, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
				}
			}
		}
		if !allow {
			return fiber.ErrForbidden
		}
		var in questionIn
		if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.QType) == "" || strings.TrimSpace(in.Prompt) == "" {
			return fiber.ErrBadRequest
		}
		// Normalize frontend constants to backend qtype
		qt := strings.ToLower(strings.TrimSpace(in.QType))
		switch qt {
		case "multiple_choice":
			qt = "mcq"
		case "multiple_select":
			qt = "mcq" // require options.allowMultiple=true for multiple
		case "true_false":
			qt = "truefalse"
		case "fill_blank":
			qt = "fillblank"
		case "short_answer":
			qt = "short"
		case "drag_drop":
			qt = "dragdrop"
		}
		pts := 1
		if in.Points != nil {
			pts = *in.Points
		}
		ord := 0
		if in.OrderIndex != nil {
			ord = *in.OrderIndex
		}
		var out testQuestion
		if err := opts.DB.Get(&out, `INSERT INTO test_questions (test_id, qtype, prompt, options, answer, points, order_index)
            VALUES ($1,$2,$3,$4,$5,$6,$7)
            RETURNING id, test_id, qtype, prompt, COALESCE(options,'null'::jsonb) AS options, COALESCE(answer,'null'::jsonb) AS answer, points, order_index`, id, qt, in.Prompt, nullIfEmptyJSON(in.Options), nullIfEmptyJSON(in.Answer), pts, ord); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})

	// --- Test Attempts ---
	type attempt struct {
		ID            string          `json:"id" db:"id"`
		TestID        string          `json:"testId" db:"test_id"`
		StudentUserID string          `json:"studentUserId" db:"student_user_id"`
		Score         *float64        `json:"score,omitempty" db:"score"`
		Status        string          `json:"status" db:"status"`
		SubmittedAt   *time.Time      `json:"submittedAt,omitempty" db:"submitted_at"`
		Responses     json.RawMessage `json:"responses,omitempty" db:"responses"`
		Grading       json.RawMessage `json:"grading,omitempty" db:"grading"`
	}

	app.Post("/v1/tests/:id/attempts/start", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		// Upsert-like: if existing in_progress, return it; else create
		var row attempt
		err = opts.DB.Get(&row, `SELECT id, test_id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading FROM test_attempts WHERE test_id=$1 AND student_user_id=$2`, id, uid)
		if err == nil {
			if row.Status == "in_progress" {
				return c.JSON(fiber.Map{"success": true, "data": row})
			}
			// existing submitted: create a new in_progress (one active at a time per test per user is typical; we'll replace)
		}
		if err := opts.DB.Get(&row, `INSERT INTO test_attempts (test_id, student_user_id, status) VALUES ($1,$2,'in_progress')
            RETURNING id, test_id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading`, id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})

	app.Patch("/v1/tests/:id/attempts/save", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var body struct {
			Responses json.RawMessage `json:"responses"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`UPDATE test_attempts SET responses=$1 WHERE test_id=$2 AND student_user_id=$3 AND status='in_progress'`, nullIfEmptyJSON(body.Responses), id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	app.Post("/v1/tests/:id/attempts/submit", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		id := c.Params("id")
		var body struct {
			Responses map[string]any `json:"responses"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		// Load questions for grading
		var qs []struct {
			ID, QType, Prompt  string
			Options, Answer    json.RawMessage
			Points, OrderIndex int
		}
		if err := opts.DB.Select(&qs, `SELECT id, qtype, prompt, COALESCE(options,'null'::jsonb) AS options, COALESCE(answer,'null'::jsonb) AS answer, points, order_index FROM test_questions WHERE test_id=$1 ORDER BY order_index, id`, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		earned := 0.0
		max := 0.0
		details := map[string]any{}

		// Helper decoders
		getBool := func(v any) (bool, bool) {
			b, ok := v.(bool)
			if ok {
				return b, true
			}
			if s, ok := v.(string); ok {
				return strings.ToLower(strings.TrimSpace(s)) == "true", true
			}
			return false, false
		}
		getStr := func(v any) (string, bool) {
			if s, ok := v.(string); ok {
				return s, true
			}
			return "", false
		}
		getStrSlice := func(v any) []string {
			if v == nil {
				return nil
			}
			if arr, ok := v.([]any); ok {
				out := []string{}
				for _, x := range arr {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
				return out
			}
			return nil
		}
		getNum := func(v any) (float64, bool) {
			switch t := v.(type) {
			case float64:
				return t, true
			case int:
				return float64(t), true
			case string:
				if f, e := strconv.ParseFloat(strings.TrimSpace(t), 64); e == nil {
					return f, true
				}
			}
			return 0, false
		}

		for _, q := range qs {
			// Only auto-grade objective types
			qt := strings.ToLower(strings.TrimSpace(q.QType))
			objective := map[string]bool{"mcq": true, "truefalse": true, "match": true, "ordering": true, "fillblank": true, "numeric": true}
			if !objective[qt] {
				continue
			}
			max += float64(q.Points)
			resp := body.Responses[q.ID]
			var opts map[string]any
			_ = json.Unmarshal(q.Options, &opts)
			var ans map[string]any
			_ = json.Unmarshal(q.Answer, &ans)
			correct := false
			partial := 0.0
			isMulti := false
			switch qt {
			case "mcq":
				allowMultiple, _ := opts["allowMultiple"].(bool)
				isMulti = allowMultiple
				if allowMultiple {
					want := getStrSlice(ans["keys"])
					got := getStrSlice(resp)
					if len(want) > 0 {
						// partial via Jaccard index: |W∩G| / |W∪G|
						wset := map[string]bool{}
						for _, k := range want {
							wset[k] = true
						}
						gset := map[string]bool{}
						for _, k := range got {
							gset[k] = true
						}
						inter := 0.0
						uni := 0.0
						// union keys
						ukeys := map[string]bool{}
						for k := range wset {
							ukeys[k] = true
						}
						for k := range gset {
							ukeys[k] = true
						}
						for k := range ukeys {
							uni += 1
							if wset[k] && gset[k] {
								inter += 1
							}
						}
						if uni > 0 {
							partial = inter / uni
						}
					}
				} else {
					w, _ := getStr(ans["key"])
					g, _ := getStr(resp)
					correct = (w != "" && w == g)
				}
			case "truefalse":
				w, _ := getBool(ans["value"])
				g, _ := getBool(resp)
				correct = (w == g)
			case "match":
				// answer.pairs: [[left,right],...]
				pairsAny, _ := ans["pairs"].([]any)
				want := map[string]string{}
				for _, p := range pairsAny {
					if arr, ok := p.([]any); ok && len(arr) >= 2 {
						l, _ := getStr(arr[0])
						r, _ := getStr(arr[1])
						if l != "" {
							want[l] = r
						}
					}
				}
				// resp can be object { leftId: rightId }
				got := map[string]string{}
				if m, ok := resp.(map[string]any); ok {
					for lk, rv := range m {
						rs, _ := getStr(rv)
						got[lk] = rs
					}
				}
				// score per-left correct
				per := 0.0
				total := float64(len(want))
				if total > 0 {
					for l, r := range want {
						if got[l] == r {
							per += 1
						}
					}
					partial = per / total
				}
			case "ordering":
				// answer.order: [id1,id2,...]
				want := getStrSlice(ans["order"])
				got := getStrSlice(resp)
				if len(want) > 1 && len(got) == len(want) {
					// adjacency-based partial: match adjacent pairs
					wp := map[string]string{}
					for i := 0; i < len(want)-1; i++ {
						wp[want[i]] = want[i+1]
					}
					matches := 0.0
					for i := 0; i < len(got)-1; i++ {
						if wp[got[i]] == got[i+1] {
							matches += 1
						}
					}
					partial = matches / float64(len(want)-1)
					if partial == 1.0 {
						correct = true
					}
				} else if len(want) > 0 && len(got) == len(want) {
					// single item or empty adjacency; require exact
					ok := true
					for i := range want {
						if want[i] != got[i] {
							ok = false
							break
						}
					}
					correct = ok
				}
			case "fillblank":
				// answer: { blankId: value }
				want := map[string]string{}
				for k, v := range ans {
					if s, ok := v.(string); ok {
						want[k] = s
					}
				}
				got := map[string]string{}
				if m, ok := resp.(map[string]any); ok {
					for k, v := range m {
						if s, ok := v.(string); ok {
							got[k] = s
						}
					}
				}
				// case-insensitive by default; synonyms optional in options.blanks[*].synonyms
				blanks, _ := opts["blanks"].([]any)
				total := float64(len(want))
				per := 0.0
				if total > 0 {
					for _, bAny := range blanks {
						if b, ok := bAny.(map[string]any); ok {
							id, _ := getStr(b["id"])
							wantVal := strings.TrimSpace(strings.ToLower(want[id]))
							gotVal := strings.TrimSpace(strings.ToLower(got[id]))
							if wantVal == "" {
								continue
							}
							if gotVal == wantVal {
								per += 1
								continue
							}
							if syn, ok := b["synonyms"].([]any); ok {
								for _, s := range syn {
									if ss, ok := s.(string); ok && strings.ToLower(strings.TrimSpace(ss)) == gotVal {
										per += 1
										break
									}
								}
							}
						}
					}
					partial = per / total
				}
			case "numeric":
				want, _ := getNum(ans["value"])
				got, ok := getNum(resp)
				if ok {
					tol := 0.0
					if r, ok := opts["tolerance"].(float64); ok {
						tol = r
					}
					if rng, ok := opts["range"].(map[string]any); ok {
						min, _ := getNum(rng["min"])
						max, _ := getNum(rng["max"])
						correct = (got >= min && got <= max)
					} else {
						correct = (math.Abs(got-want) <= tol)
					}
				}
			}
			// accumulate
			if qt == "match" || qt == "fillblank" || (qt == "mcq" && isMulti) || qt == "ordering" {
				earned += float64(q.Points) * partial
			} else if correct {
				earned += float64(q.Points)
			}
			details[q.ID] = map[string]any{"correct": correct, "partial": partial}
		}
		var score *float64
		if max > 0 {
			s := (earned / max) * 100.0
			score = &s
		}
		gradingJSON, _ := json.Marshal(fiber.Map{"earned": earned, "max": max, "score": score})
		respsJSON, _ := json.Marshal(body.Responses)
		if _, err := opts.DB.Exec(`UPDATE test_attempts SET responses=$1, grading=$2, score=$3, status='submitted', submitted_at=now() WHERE test_id=$4 AND student_user_id=$5`, respsJSON, gradingJSON, score, id, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"earned": earned, "max": max, "score": score}})
	})

	// --- Guardians ---
	app.Get("/v1/guardians/children", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var rows []struct {
			Child string `json:"childUserId" db:"child_user_id"`
		}
		if err := opts.DB.Select(&rows, `SELECT child_user_id FROM guardians_children WHERE guardian_user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/guardians/link", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			ChildUserID string `json:"childUserId"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.ChildUserID) == "" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO guardians_children (guardian_user_id, child_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, body.ChildUserID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Progress ---
	app.Get("/v1/progress/overview", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		userID := c.Query("user_id", uid)
		if userID != uid {
			var ok bool
			if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, userID); err != nil {
				return fiber.ErrForbidden
			}
			if !ok {
				return fiber.ErrForbidden
			}
		}
		var classes int
		_ = opts.DB.Get(&classes, `SELECT COUNT(*) FROM class_enrollments WHERE student_user_id=$1`, userID)
		var completed int
		_ = opts.DB.Get(&completed, `SELECT COUNT(*) FROM lesson_progress WHERE student_user_id=$1 AND status='completed'`, userID)
		var attempted int
		_ = opts.DB.Get(&attempted, `SELECT COUNT(*) FROM test_attempts WHERE student_user_id=$1 AND status<>'in_progress'`, userID)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"classesEnrolled": classes, "lessonsCompleted": completed, "testsAttempted": attempted}})
	})
	// List lesson progress for current user (optionally guardian viewing a child)
	app.Get("/v1/progress/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		userID := c.Query("user_id", uid)
		if userID != uid {
			var ok bool
			if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, userID); err != nil {
				return fiber.ErrForbidden
			}
			if !ok {
				return fiber.ErrForbidden
			}
		}
		status := c.Query("status")
		if status != "" && status != "in_progress" && status != "completed" {
			return fiber.ErrBadRequest
		}
		type row struct {
			LessonID string `json:"lessonId" db:"lesson_id"`
			Status   string `json:"status" db:"status"`
		}
		rows := []row{}
		var q string
		if status == "" {
			q = `SELECT lesson_id, status FROM lesson_progress WHERE student_user_id=$1 ORDER BY updated_at DESC LIMIT 100`
			if err := opts.DB.Select(&rows, q, userID); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			q = `SELECT lesson_id, status FROM lesson_progress WHERE student_user_id=$1 AND status=$2 ORDER BY updated_at DESC LIMIT 100`
			if err := opts.DB.Select(&rows, q, userID, status); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- School Staff Management ---
	app.Get("/v1/schools/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		type row struct {
			ID        string    `json:"id" db:"id"`
			UserID    string    `json:"userId" db:"user_id"`
			Role      string    `json:"role" db:"role"`
			Status    string    `json:"status" db:"status"`
			CreatedAt time.Time `json:"createdAt" db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, user_id, role, status, created_at FROM school_members WHERE school_id=$1 ORDER BY created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		var body struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		r := strings.TrimSpace(strings.ToLower(body.Role))
		if body.UserID == "" || (r != "admin" && r != "tutor") {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role, status) VALUES ($1,$2,$3,'active')
            ON CONFLICT (school_id, user_id, role) DO UPDATE SET status='active'`, sid, body.UserID, r); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// List pending invites for a school (admin only)
	app.Get("/v1/schools/:id/invites", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		type row struct {
			ID          string     `json:"id" db:"id"`
			Email       *string    `json:"email" db:"email"`
			ExistingUID *string    `json:"existingUserId" db:"existing_user_id"`
			Role        string     `json:"role" db:"role"`
			Status      string     `json:"status" db:"status"`
			InvitedBy   string     `json:"invitedByUserId" db:"invited_by_user_id"`
			Message     *string    `json:"message" db:"message"`
			CreatedAt   time.Time  `json:"createdAt" db:"created_at"`
			RespondedAt *time.Time `json:"respondedAt" db:"responded_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, email, existing_user_id, role, status, invited_by_user_id, message, created_at, responded_at
            FROM school_invites WHERE school_id=$1 ORDER BY created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Create an invite for a school (admin only)
	app.Post("/v1/schools/:id/invites", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}
		var body struct {
			Email          string  `json:"email"`
			ExistingUserID string  `json:"existingUserId"`
			Role           string  `json:"role"`
			Message        *string `json:"message"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		role := strings.TrimSpace(strings.ToLower(body.Role))
		if role != "admin" && role != "tutor" {
			return fiber.ErrBadRequest
		}
		email := strings.TrimSpace(body.Email)
		existingUID := strings.TrimSpace(body.ExistingUserID)
		if email == "" && existingUID == "" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Email or existingUserId is required"})
		}
		token := uuid.NewString()
		var message *string
		if body.Message != nil {
			m := strings.TrimSpace(*body.Message)
			if m != "" {
				message = &m
			}
		}
		var out struct {
			ID          string    `json:"id"`
			Email       *string   `json:"email"`
			ExistingUID *string   `json:"existingUserId"`
			Role        string    `json:"role"`
			Status      string    `json:"status"`
			Token       string    `json:"token"`
			Message     *string   `json:"message"`
			CreatedAt   time.Time `json:"createdAt"`
			InvitedBy   string    `json:"invitedByUserId"`
		}
		if err := opts.DB.Get(&out, `INSERT INTO school_invites (school_id, email, existing_user_id, role, invited_by_user_id, token, message)
				VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, $5, $6, $7)
				RETURNING id, email, existing_user_id, role, status, token, message, created_at, invited_by_user_id`,
			sid, email, existingUID, role, uid, token, message); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	// Detailed school overview for administrators
	app.Get("/v1/schools/:id/overview", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		sid := c.Params("id")
		okA, _ := auth.IsSchoolAdmin(opts.DB, sid, uid)
		if !okA {
			return fiber.ErrForbidden
		}

		var school struct {
			ID          string    `json:"id" db:"id"`
			OwnerUserID string    `json:"ownerUserId" db:"owner_user_id"`
			Name        string    `json:"name" db:"name"`
			Description *string   `json:"description,omitempty" db:"description"`
			IsVerified  bool      `json:"isVerified" db:"is_verified"`
			CreatedAt   time.Time `json:"createdAt" db:"created_at"`
			UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
		}
		if err := opts.DB.Get(&school, `SELECT id, owner_user_id, name, description, is_verified, created_at, updated_at FROM schools WHERE id=$1`, sid); err != nil {
			return fiber.ErrNotFound
		}

		type classRow struct {
			ID           string    `db:"id"`
			Title        string    `db:"title"`
			Description  *string   `db:"description"`
			TutorUserID  string    `db:"tutor_user_id"`
			TutorName    *string   `db:"tutor_name"`
			Visibility   string    `db:"visibility"`
			IsPaid       bool      `db:"is_paid"`
			PriceCents   int       `db:"price_cents"`
			StudentCount int       `db:"student_count"`
			CreatedAt    time.Time `db:"created_at"`
		}
		classRows := []classRow{}
		if err := opts.DB.Select(&classRows, `
            SELECT c.id, c.title, c.description, c.tutor_user_id,
                   up.display_name AS tutor_name, c.visibility, c.is_paid, c.price_cents,
                   COALESCE(stats.student_count, 0) AS student_count, c.created_at
            FROM classes c
            LEFT JOIN (
                SELECT class_id, SUM(CASE WHEN status='active' THEN 1 ELSE 0 END)::int AS student_count
                FROM class_enrollments
                GROUP BY class_id
            ) stats ON stats.class_id=c.id
            LEFT JOIN user_profiles up ON up.user_id=c.tutor_user_id
            WHERE c.school_id=$1
            ORDER BY c.created_at DESC`, sid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}

		classIDs := make([]string, 0, len(classRows))
		for _, cr := range classRows {
			classIDs = append(classIDs, cr.ID)
		}

		type studentRow struct {
			ClassID     string  `db:"class_id"`
			UserID      string  `db:"student_user_id"`
			DisplayName *string `db:"display_name"`
		}
		studentRows := []studentRow{}
		if len(classIDs) > 0 {
			query, args, _ := sqlx.In(`SELECT e.class_id, e.student_user_id, up.display_name
                FROM class_enrollments e
                LEFT JOIN user_profiles up ON up.user_id=e.student_user_id
                WHERE e.status='active' AND e.class_id IN (?)
                ORDER BY up.display_name NULLS LAST, e.student_user_id`, classIDs)
			query = opts.DB.Rebind(query)
			if err := opts.DB.Select(&studentRows, query, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}

		type subjectRow struct {
			ID          string  `db:"id"`
			ClassID     string  `db:"class_id"`
			Title       string  `db:"title"`
			Description *string `db:"description"`
			OrderIndex  int     `db:"order_index"`
		}
		subjectRows := []subjectRow{}
		if len(classIDs) > 0 {
			query, args, _ := sqlx.In(`SELECT id, class_id, title, description, order_index
                FROM subjects WHERE class_id IN (?)
                ORDER BY order_index, title`, classIDs)
			query = opts.DB.Rebind(query)
			if err := opts.DB.Select(&subjectRows, query, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}

		subjectIDs := make([]string, 0, len(subjectRows))
		for _, sr := range subjectRows {
			subjectIDs = append(subjectIDs, sr.ID)
		}

		type lessonRow struct {
			ID         string          `db:"id"`
			SubjectID  string          `db:"subject_id"`
			Title      string          `db:"title"`
			Type       string          `db:"type"`
			Content    json.RawMessage `db:"content"`
			OrderIndex int             `db:"order_index"`
			IsFree     bool            `db:"is_free"`
		}
		lessonRows := []lessonRow{}
		if len(subjectIDs) > 0 {
			query, args, _ := sqlx.In(`SELECT id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free
                FROM lessons WHERE subject_id IN (?)
                ORDER BY order_index, title`, subjectIDs)
			query = opts.DB.Rebind(query)
			if err := opts.DB.Select(&lessonRows, query, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}

		lessonIDs := make([]string, 0, len(lessonRows))
		for _, lr := range lessonRows {
			lessonIDs = append(lessonIDs, lr.ID)
		}

		type testRow struct {
			ID          string    `db:"id"`
			SubjectID   *string   `db:"subject_id"`
			LessonID    *string   `db:"lesson_id"`
			Title       string    `db:"title"`
			Description *string   `db:"description"`
			Visibility  string    `db:"visibility"`
			CreatedAt   time.Time `db:"created_at"`
		}
		testRows := []testRow{}
		if len(subjectIDs) > 0 || len(lessonIDs) > 0 {
			var queries []string
			var args []any
			if len(subjectIDs) > 0 {
				q, a, _ := sqlx.In(`SELECT id, subject_id, lesson_id, title, description, visibility, created_at
                    FROM tests WHERE subject_id IN (?)`, subjectIDs)
				queries = append(queries, q)
				args = append(args, a...)
			}
			if len(lessonIDs) > 0 {
				q, a, _ := sqlx.In(`SELECT id, subject_id, lesson_id, title, description, visibility, created_at
                    FROM tests WHERE lesson_id IN (?)`, lessonIDs)
				queries = append(queries, q)
				args = append(args, a...)
			}
			if len(queries) > 0 {
				combined := strings.Join(queries, " UNION ALL ")
				combined = opts.DB.Rebind(combined + " ORDER BY created_at DESC")
				if err := opts.DB.Select(&testRows, combined, args...); err != nil {
					return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
				}
			}
		}

		// Assemble response
		classMap := map[string]fiber.Map{}
		for _, cr := range classRows {
			tutor := fiber.Map{"userId": cr.TutorUserID}
			if cr.TutorName != nil && strings.TrimSpace(*cr.TutorName) != "" {
				tutor["displayName"] = *cr.TutorName
			}
			classMap[cr.ID] = fiber.Map{
				"id":           cr.ID,
				"title":        cr.Title,
				"description":  cr.Description,
				"tutor":        tutor,
				"visibility":   cr.Visibility,
				"isPaid":       cr.IsPaid,
				"priceCents":   cr.PriceCents,
				"studentCount": cr.StudentCount,
				"students":     []fiber.Map{},
				"subjects":     []fiber.Map{},
			}
		}
		for _, sr := range studentRows {
			cls, ok := classMap[sr.ClassID]
			if !ok {
				continue
			}
			students := cls["students"].([]fiber.Map)
			display := strings.TrimSpace(coalesceString(sr.DisplayName))
			if display == "" {
				display = sr.UserID
			}
			students = append(students, fiber.Map{"userId": sr.UserID, "displayName": display})
			cls["students"] = students
		}
		subjectMap := map[string]fiber.Map{}
		for _, sr := range subjectRows {
			subj := fiber.Map{
				"id":          sr.ID,
				"title":       sr.Title,
				"description": sr.Description,
				"orderIndex":  sr.OrderIndex,
				"lessons":     []fiber.Map{},
				"tests":       []fiber.Map{},
			}
			subjectMap[sr.ID] = subj
			if cls, ok := classMap[sr.ClassID]; ok {
				subs := cls["subjects"].([]fiber.Map)
				subs = append(subs, subj)
				cls["subjects"] = subs
			}
		}
		lessonMap := map[string]fiber.Map{}
		for _, lr := range lessonRows {
			lesson := fiber.Map{
				"id":         lr.ID,
				"title":      lr.Title,
				"type":       lr.Type,
				"content":    lr.Content,
				"orderIndex": lr.OrderIndex,
				"isFree":     lr.IsFree,
				"tests":      []fiber.Map{},
			}
			lessonMap[lr.ID] = lesson
			if subj, ok := subjectMap[lr.SubjectID]; ok {
				lessons := subj["lessons"].([]fiber.Map)
				lessons = append(lessons, lesson)
				subj["lessons"] = lessons
			}
		}
		for _, tr := range testRows {
			test := fiber.Map{
				"id":          tr.ID,
				"title":       tr.Title,
				"description": tr.Description,
				"visibility":  tr.Visibility,
				"createdAt":   tr.CreatedAt,
			}
			if tr.LessonID != nil {
				if lesson, ok := lessonMap[*tr.LessonID]; ok {
					tests := lesson["tests"].([]fiber.Map)
					tests = append(tests, test)
					lesson["tests"] = tests
					continue
				}
			}
			if tr.SubjectID != nil {
				if subj, ok := subjectMap[*tr.SubjectID]; ok {
					tests := subj["tests"].([]fiber.Map)
					tests = append(tests, test)
					subj["tests"] = tests
				}
			}
		}

		classes := []fiber.Map{}
		for _, cr := range classRows {
			if cls, ok := classMap[cr.ID]; ok {
				classes = append(classes, cls)
			}
		}

		return c.JSON(fiber.Map{
			"success": true,
			"data": fiber.Map{
				"school":  school,
				"classes": classes,
			},
		})
	})

	// --- Private Tutor Applications ---
	app.Post("/v1/tutors/apply", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body map[string]any
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		// Draft flag allows partial save without strict validation
		draft := false
		if v, ok := body["draft"]; ok {
			switch b := v.(type) {
			case bool:
				draft = b
			case string:
				draft = strings.EqualFold(b, "true") || b == "1"
			}
		}
		// Validate required fields according to onboarding checklist
		gs := func(m map[string]any, keys ...string) string {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return ""
				}
				cur, ok = mm[k]
				if !ok {
					return ""
				}
			}
			if s, ok := cur.(string); ok {
				return strings.TrimSpace(s)
			}
			return ""
		}
		getb := func(m map[string]any, keys ...string) bool {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return false
				}
				cur, ok = mm[k]
				if !ok {
					return false
				}
			}
			if b, ok := cur.(bool); ok {
				return b
			}
			return false
		}
		if !draft {
			missing := []string{}
			// Personal Info
			if gs(body, "profile", "legalName") == "" {
				missing = append(missing, "profile.legalName")
			}
			if gs(body, "profile", "displayName") == "" {
				missing = append(missing, "profile.displayName")
			}
			if gs(body, "profile", "email") == "" {
				missing = append(missing, "profile.email")
			}
			if gs(body, "profile", "phone") == "" {
				missing = append(missing, "profile.phone")
			}
			if gs(body, "profile", "country") == "" {
				missing = append(missing, "profile.country")
			}
			if gs(body, "profile", "languages") == "" {
				missing = append(missing, "profile.languages")
			}
			if gs(body, "profile", "timezone") == "" {
				missing = append(missing, "profile.timezone")
			}
			if s, _ := body["bio"].(string); strings.TrimSpace(s) == "" {
				missing = append(missing, "bio")
			}
			// Identity Verification
			if gs(body, "verification", "govIdType") == "" {
				missing = append(missing, "verification.govIdType")
			}
			if gs(body, "verification", "govIdUrl") == "" {
				missing = append(missing, "verification.govIdUrl")
			}
			if gs(body, "verification", "selfieUrl") == "" {
				missing = append(missing, "verification.selfieUrl")
			}
			// Education Proof
			if gs(body, "education", "degreeUrl") == "" {
				missing = append(missing, "education.degreeUrl")
			}
			// Demo content
			if gs(body, "media", "introUrl") == "" {
				missing = append(missing, "media.introUrl")
			}
			if gs(body, "media", "demoUrl") == "" {
				missing = append(missing, "media.demoUrl")
			}
			// Consents
			if !getb(body, "consents", "backgroundCheck") {
				missing = append(missing, "consents.backgroundCheck")
			}
			if !getb(body, "consents", "codeOfConduct") {
				missing = append(missing, "consents.codeOfConduct")
			}
			if !getb(body, "consents", "cleanRecord") {
				missing = append(missing, "consents.cleanRecord")
			}
			if !getb(body, "consents", "lessonRecording") {
				missing = append(missing, "consents.lessonRecording")
			}
			// Payout
			if gs(body, "payout", "method") == "" {
				missing = append(missing, "payout.method")
			}
			if gs(body, "payout", "name") == "" {
				missing = append(missing, "payout.name")
			}
			if len(missing) > 0 {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Missing required fields", "missing": missing})
			}
		}
		profile := body["profile"]
		verification := body["verification"]
		education := body["education"]
		teaching := body["teaching"]
		media := body["media"]
		payout := body["payout"]
		consents := body["consents"]
		bio := body["bio"]
		subjects := body["subjects"]
		// optional progress percentage
		var progress *int
		if v, ok := body["progress"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		if v, ok := body["progressPct"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		desiredStatus := "pending"
		if draft {
			desiredStatus = "draft"
		}
		if _, err := opts.DB.Exec(`INSERT INTO private_tutors (user_id, status, bio, subjects, profile, verification, education, teaching, media, payout, consents, progress_pct)
            VALUES ($1,$12,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE($11,0))
            ON CONFLICT (user_id) DO UPDATE SET status='pending', bio=COALESCE(EXCLUDED.bio, private_tutors.bio), subjects=COALESCE(EXCLUDED.subjects, private_tutors.subjects),
                profile=COALESCE(EXCLUDED.profile, private_tutors.profile), verification=COALESCE(EXCLUDED.verification, private_tutors.verification),
                education=COALESCE(EXCLUDED.education, private_tutors.education), teaching=COALESCE(EXCLUDED.teaching, private_tutors.teaching),
                media=COALESCE(EXCLUDED.media, private_tutors.media), payout=COALESCE(EXCLUDED.payout, private_tutors.payout), consents=COALESCE(EXCLUDED.consents, private_tutors.consents),
                progress_pct=COALESCE(EXCLUDED.progress_pct, private_tutors.progress_pct), updated_at=now()`,
			uid, bio, subjects, profile, verification, education, teaching, media, payout, consents, progress, desiredStatus); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Submit draft -> pending (validates)
	app.Post("/v1/tutors/submit", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		// Reuse /me to fetch draft and ensure presence
		var row struct {
			Profile      any     `db:"profile"`
			Verification any     `db:"verification"`
			Education    any     `db:"education"`
			Teaching     any     `db:"teaching"`
			Media        any     `db:"media"`
			Payout       any     `db:"payout"`
			Consents     any     `db:"consents"`
			Bio          *string `db:"bio"`
			Subjects     *string `db:"subjects"`
		}
		if err := opts.DB.Get(&row, `SELECT profile, verification, education, teaching, media, payout, consents, bio, subjects FROM private_tutors WHERE user_id=$1`, uid); err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "No draft found"})
		}
		// Minimal gate: require status change only if currently draft
		var cur string
		_ = opts.DB.Get(&cur, `SELECT status FROM private_tutors WHERE user_id=$1`, uid)
		if cur != "draft" && cur != "rejected" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Not in draft/rejected state"})
		}
		// Promote to pending
		if _, err := opts.DB.Exec(`UPDATE private_tutors SET status='pending', updated_at=now() WHERE user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/tutors/me", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var row struct {
			UserID       string    `json:"userId" db:"user_id"`
			Status       string    `json:"status" db:"status"`
			Bio          *string   `json:"bio,omitempty" db:"bio"`
			Subjects     *string   `json:"subjects,omitempty" db:"subjects"`
			Profile      any       `json:"profile,omitempty" db:"profile"`
			Verification any       `json:"verification,omitempty" db:"verification"`
			Education    any       `json:"education,omitempty" db:"education"`
			Teaching     any       `json:"teaching,omitempty" db:"teaching"`
			Media        any       `json:"media,omitempty" db:"media"`
			Payout       any       `json:"payout,omitempty" db:"payout"`
			Consents     any       `json:"consents,omitempty" db:"consents"`
			Progress     int       `json:"progressPct" db:"progress_pct"`
			CreatedAt    time.Time `json:"createdAt" db:"created_at"`
			UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
		}
		err = opts.DB.Get(&row, `SELECT user_id, status, bio, subjects, profile, verification, education, teaching, media, payout, consents, progress_pct, created_at, updated_at FROM private_tutors WHERE user_id=$1`, uid)
		if err != nil {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})

	// --- School Applications ---
	// Create/update application (draft or pending)
	app.Post("/v1/schools/apply", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body map[string]any
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		draft := false
		if v, ok := body["draft"]; ok {
			switch b := v.(type) {
			case bool:
				draft = b
			case string:
				draft = strings.EqualFold(b, "true") || b == "1"
			}
		}
		// helpers
		gs := func(m map[string]any, keys ...string) string {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return ""
				}
				cur, ok = mm[k]
				if !ok {
					return ""
				}
			}
			if s, ok := cur.(string); ok {
				return strings.TrimSpace(s)
			}
			return ""
		}
		getArrLen := func(m map[string]any, keys ...string) int {
			cur := any(m)
			for _, k := range keys {
				mm, ok := cur.(map[string]any)
				if !ok {
					return 0
				}
				cur, ok = mm[k]
				if !ok {
					return 0
				}
			}
			if a, ok := cur.([]any); ok {
				return len(a)
			}
			return 0
		}
		if !draft {
			missing := []string{}
			if gs(body, "info", "name") == "" {
				missing = append(missing, "info.name")
			}
			if gs(body, "info", "country") == "" {
				missing = append(missing, "info.country")
			}
			if gs(body, "info", "businessType") == "" {
				missing = append(missing, "info.businessType")
			}
			if gs(body, "info", "contact", "name") == "" {
				missing = append(missing, "info.contact.name")
			}
			if gs(body, "info", "contact", "email") == "" {
				missing = append(missing, "info.contact.email")
			}
			if gs(body, "info", "contact", "phone") == "" {
				missing = append(missing, "info.contact.phone")
			}
			if gs(body, "info", "description") == "" {
				missing = append(missing, "info.description")
			}
			if gs(body, "verify", "registrationCertUrl") == "" {
				missing = append(missing, "verify.registrationCertUrl")
			}
			if gs(body, "verify", "taxPinUrl") == "" {
				missing = append(missing, "verify.taxPinUrl")
			}
			if gs(body, "verify", "proofAddressUrl") == "" {
				missing = append(missing, "verify.proofAddressUrl")
			}
			if gs(body, "verify", "founderIdUrl") == "" {
				missing = append(missing, "verify.founderIdUrl")
			}
			if getArrLen(body, "staff", "tutors") <= 0 {
				missing = append(missing, "staff.tutors")
			}
			if gs(body, "finance", "payoutMethod") == "" {
				missing = append(missing, "finance.payoutMethod")
			}
			if gs(body, "finance", "currency") == "" {
				missing = append(missing, "finance.currency")
			}
			if gs(body, "finance", "bankDetails") == "" {
				missing = append(missing, "finance.bankDetails")
			}
			if gs(body, "finance", "revenueModel") == "" {
				missing = append(missing, "finance.revenueModel")
			}
			if gs(body, "curriculum", "subjects") == "" {
				missing = append(missing, "curriculum.subjects")
			}
			if gs(body, "curriculum", "targets") == "" {
				missing = append(missing, "curriculum.targets")
			}
			if gs(body, "curriculum", "format") == "" {
				missing = append(missing, "curriculum.format")
			}
			if gs(body, "curriculum", "languages") == "" {
				missing = append(missing, "curriculum.languages")
			}
			if gs(body, "curriculum", "demoUrl") == "" {
				missing = append(missing, "curriculum.demoUrl")
			}
			// agreements are booleans; check present and true
			needTrue := []string{"partnership", "privacy", "revenueSplit", "codeOfConduct", "quality", "antiFraud", "refund"}
			agr, _ := body["agreements"].(map[string]any)
			for _, k := range needTrue {
				if v, ok := agr[k]; !ok || v != true {
					missing = append(missing, "agreements."+k)
				}
			}
			if len(missing) > 0 {
				return c.Status(400).JSON(fiber.Map{"success": false, "message": "Missing required fields", "missing": missing})
			}
		}
		var progress *int
		if v, ok := body["progress"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		if v, ok := body["progressPct"].(float64); ok {
			p := int(v)
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			progress = &p
		}
		status := "pending"
		if draft {
			status = "draft"
		}
		info := body["info"]
		verify := body["verify"]
		staff := body["staff"]
		finance := body["finance"]
		curriculum := body["curriculum"]
		agreements := body["agreements"]
		extras := body["extras"]
		if _, err := opts.DB.Exec(`INSERT INTO school_applications (owner_user_id, status, info, verify, staff, finance, curriculum, agreements, extras, progress_pct)
            VALUES ($1,$10,$2,$3,$4,$5,$6,$7,$8,COALESCE($9,0))
            ON CONFLICT (owner_user_id) DO UPDATE SET status='pending', info=COALESCE(EXCLUDED.info, school_applications.info), verify=COALESCE(EXCLUDED.verify, school_applications.verify),
              staff=COALESCE(EXCLUDED.staff, school_applications.staff), finance=COALESCE(EXCLUDED.finance, school_applications.finance), curriculum=COALESCE(EXCLUDED.curriculum, school_applications.curriculum),
              agreements=COALESCE(EXCLUDED.agreements, school_applications.agreements), extras=COALESCE(EXCLUDED.extras, school_applications.extras), progress_pct=COALESCE(EXCLUDED.progress_pct, school_applications.progress_pct), updated_at=now()`,
			uid, info, verify, staff, finance, curriculum, agreements, extras, progress, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Submit draft -> pending
	app.Post("/v1/schools/applications/submit", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var cur string
		_ = opts.DB.Get(&cur, `SELECT status FROM school_applications WHERE owner_user_id=$1`, uid)
		if cur != "draft" && cur != "rejected" {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Not in draft/rejected state"})
		}
		if _, err := opts.DB.Exec(`UPDATE school_applications SET status='pending', updated_at=now() WHERE owner_user_id=$1`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	// Current user's application
	app.Get("/v1/schools/applications/me", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var row struct {
			ID         string `json:"id" db:"id"`
			Owner      string `json:"ownerUserId" db:"owner_user_id"`
			Status     string `json:"status" db:"status"`
			Info       any    `json:"info" db:"info"`
			Verify     any    `json:"verify" db:"verify"`
			Staff      any    `json:"staff" db:"staff"`
			Finance    any    `json:"finance" db:"finance"`
			Curriculum any    `json:"curriculum" db:"curriculum"`
			Agreements any    `json:"agreements" db:"agreements"`
			Extras     any    `json:"extras" db:"extras"`
			Progress   int    `json:"progressPct" db:"progress_pct"`
		}
		if err := opts.DB.Get(&row, `SELECT id, owner_user_id, status, info, verify, staff, finance, curriculum, agreements, extras, progress_pct FROM school_applications WHERE owner_user_id=$1`, uid); err != nil {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": row})
	})
	// Platform review of school applications
	app.Get("/v1/schools/applications", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		status := strings.TrimSpace(strings.ToLower(c.Query("status")))
		if status == "" {
			status = "pending"
		}
		type row struct {
			ID          string    `json:"id" db:"id"`
			OwnerUserID string    `json:"ownerUserId" db:"owner_user_id"`
			SchoolName  *string   `json:"schoolName" db:"school_name"`
			SubmittedAt time.Time `json:"submittedAt" db:"updated_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, owner_user_id, info->>'name' AS school_name, updated_at FROM school_applications WHERE status=$1 ORDER BY updated_at DESC LIMIT 200`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/schools/applications/:id/approve", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		id := c.Params("id")
		// Fetch owner and info
		var owner string
		var infoRaw []byte
		if err := opts.DB.Get(&owner, `SELECT owner_user_id FROM school_applications WHERE id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		if err := opts.DB.Get(&infoRaw, `SELECT info FROM school_applications WHERE id=$1`, id); err != nil {
			return fiber.ErrBadRequest
		}
		var info map[string]any
		_ = json.Unmarshal(infoRaw, &info)
		name, _ := info["name"].(string)
		desc, _ := info["description"].(string)
		var descPtr *string
		if strings.TrimSpace(desc) != "" {
			d := desc
			descPtr = &d
		}
		// Create school row if not exists with same owner and name
		var sid string
		err = opts.DB.Get(&sid, `INSERT INTO schools (owner_user_id, name, description, is_verified) VALUES ($1,$2,$3,true) RETURNING id`, owner, name, descPtr)
		if err != nil {
			// If duplicate by unique constraints not present, try to find existing
			_ = opts.DB.Get(&sid, `SELECT id FROM schools WHERE owner_user_id=$1 AND name=$2`, owner, name)
		}
		// Mark application approved
		if _, err := opts.DB.Exec(`UPDATE school_applications SET status='approved', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE id=$2`, uid, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "schoolId": sid})
	})
	app.Post("/v1/schools/applications/:id/reject", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		id := c.Params("id")
		var body struct {
			Reason *string `json:"reason"`
		}
		_ = c.BodyParser(&body)
		// write decision note into verify JSON
		_, _ = opts.DB.Exec(`UPDATE school_applications SET verify = COALESCE(verify,'{}'::jsonb) || jsonb_build_object('decision','rejected','reason',COALESCE($1,'')), updated_at=now() WHERE id=$2`, body.Reason, id)
		if _, err := opts.DB.Exec(`UPDATE school_applications SET status='rejected', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE id=$2`, uid, id); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// Platform review of tutor applications (platform admin only)
	app.Get("/v1/tutors/applications", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		status := c.Query("status", "pending")
		type row struct {
			UserID      string    `json:"userId" db:"user_id"`
			Status      string    `json:"status" db:"status"`
			DisplayName *string   `json:"displayName,omitempty" db:"display_name"`
			Email       *string   `json:"email,omitempty" db:"email"`
			SubmittedAt time.Time `json:"submittedAt" db:"updated_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT pt.user_id, pt.status, up.display_name, up.email, pt.updated_at
            FROM private_tutors pt LEFT JOIN user_profiles up ON up.user_id=pt.user_id WHERE pt.status=$1 ORDER BY pt.updated_at DESC LIMIT 200`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/tutors/:userId/approve", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		tid := c.Params("userId")
		if _, err := opts.DB.Exec(`UPDATE private_tutors SET status='approved', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE user_id=$2`, uid, tid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/tutors/:userId/reject", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		tid := c.Params("userId")
		var body struct {
			Reason *string `json:"reason"`
		}
		_ = c.BodyParser(&body)
		// store decision in verification json for traceability
		_, _ = opts.DB.Exec(`UPDATE private_tutors SET verification = COALESCE(verification,'{}'::jsonb) || jsonb_build_object('decision','rejected','reason',COALESCE($1,'')), updated_at=now() WHERE user_id=$2`, body.Reason, tid)
		if _, err := opts.DB.Exec(`UPDATE private_tutors SET status='rejected', reviewed_by_user_id=$1, reviewed_at=now(), updated_at=now() WHERE user_id=$2`, uid, tid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/tutors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		status := c.Query("status", "approved")
		type row struct {
			UserID   string  `json:"userId" db:"user_id"`
			Status   string  `json:"status" db:"status"`
			Bio      *string `json:"bio,omitempty" db:"bio"`
			Subjects *string `json:"subjects,omitempty" db:"subjects"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT user_id, status, bio, subjects FROM private_tutors WHERE status=$1 ORDER BY updated_at DESC`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/progress/lessons", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			LessonID string `json:"lessonId"`
			Status   string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.LessonID) == "" {
			return fiber.ErrBadRequest
		}
		st := body.Status
		if st == "" {
			st = "in_progress"
		}
		if st != "in_progress" && st != "completed" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO lesson_progress (lesson_id, student_user_id, status, last_viewed_at, updated_at)
          VALUES ($1,$2,$3,now(),now())
          ON CONFLICT (lesson_id, student_user_id) DO UPDATE SET status=EXCLUDED.status, last_viewed_at=now(), updated_at=now()`, body.LessonID, uid, st); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Ratings ---
	app.Post("/v1/ratings/tutors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			TutorUserID string  `json:"tutorUserId"`
			Rating      int     `json:"rating"`
			Comment     *string `json:"comment"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.TutorUserID) == "" || body.Rating < 1 || body.Rating > 5 {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO tutor_ratings (tutor_user_id, rater_user_id, rating, comment) VALUES ($1,$2,$3,$4)
            ON CONFLICT (tutor_user_id, rater_user_id) DO UPDATE SET rating=EXCLUDED.rating, comment=EXCLUDED.comment, created_at=now()`, body.TutorUserID, uid, body.Rating, body.Comment); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/ratings/tutors", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		tutorID := c.Query("tutor_user_id")
		if tutorID == "" {
			return fiber.ErrBadRequest
		}
		var avg float64
		var cnt int
		_ = opts.DB.Get(&avg, `SELECT COALESCE(AVG(rating)::float,0) FROM tutor_ratings WHERE tutor_user_id=$1`, tutorID)
		_ = opts.DB.Get(&cnt, `SELECT COUNT(*) FROM tutor_ratings WHERE tutor_user_id=$1`, tutorID)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"tutorUserId": tutorID, "avgRating": avg, "ratingsCount": cnt}})
	})
	app.Get("/v1/ratings/tutors/top", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		limit := 5
		if v := strings.TrimSpace(c.Query("limit")); v != "" {
			if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil {
			}
		}
		uid, _ := getUserID(c)
		type row struct {
			TutorUserID string  `json:"tutorUserId" db:"tutor_user_id"`
			Avg         float64 `json:"avgRating" db:"avg_rating"`
			Cnt         int     `json:"ratingsCount" db:"ratings_count"`
		}
		rows := []row{}
		if uid != "" {
			if err := opts.DB.Select(&rows, `SELECT tr.tutor_user_id, AVG(tr.rating)::float AS avg_rating, COUNT(*) AS ratings_count
                FROM tutor_ratings tr
                WHERE NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_user_id=$1 AND b.blocked_user_id=tr.tutor_user_id)
                GROUP BY tr.tutor_user_id HAVING COUNT(*)>0
                ORDER BY AVG(tr.rating) DESC, COUNT(*) DESC LIMIT $2`, uid, limit); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		if err := opts.DB.Select(&rows, `SELECT tutor_user_id, AVG(rating)::float AS avg_rating, COUNT(*) AS ratings_count
            FROM tutor_ratings GROUP BY tutor_user_id HAVING COUNT(*)>0 ORDER BY AVG(rating) DESC, COUNT(*) DESC LIMIT $1`, limit); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	app.Post("/v1/ratings/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			SchoolID string  `json:"schoolId"`
			Rating   int     `json:"rating"`
			Comment  *string `json:"comment"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.SchoolID) == "" || body.Rating < 1 || body.Rating > 5 {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO school_ratings (school_id, rater_user_id, rating, comment) VALUES ($1,$2,$3,$4)
            ON CONFLICT (school_id, rater_user_id) DO UPDATE SET rating=EXCLUDED.rating, comment=EXCLUDED.comment, created_at=now()`, body.SchoolID, uid, body.Rating, body.Comment); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/ratings/schools", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		schoolID := c.Query("school_id")
		if schoolID == "" {
			return fiber.ErrBadRequest
		}
		var avg float64
		var cnt int
		_ = opts.DB.Get(&avg, `SELECT COALESCE(AVG(rating)::float,0) FROM school_ratings WHERE school_id=$1`, schoolID)
		_ = opts.DB.Get(&cnt, `SELECT COUNT(*) FROM school_ratings WHERE school_id=$1`, schoolID)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"schoolId": schoolID, "avgRating": avg, "ratingsCount": cnt}})
	})

	// --- Public Leaderboards ---
	app.Get("/v1/leaderboards/public", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		limit := 10
		if v := strings.TrimSpace(c.Query("limit")); v != "" {
			if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil {
			}
		}
		uid, _ := getUserID(c)
		subjectID := strings.TrimSpace(c.Query("subject_id"))
		type row struct {
			StudentUserID string  `json:"studentUserId" db:"student_user_id"`
			Score         float64 `json:"score" db:"score"`
			TestID        string  `json:"testId" db:"test_id"`
			SubjectID     *string `json:"subjectId,omitempty" db:"subject_id"`
		}
		rows := []row{}
		var err error
		if subjectID != "" {
			if uid != "" {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND t.subject_id=$1 AND a.score IS NOT NULL
                      AND NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_user_id=$2 AND b.blocked_user_id=a.student_user_id)
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $3`, subjectID, uid, limit)
			} else {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND t.subject_id=$1 AND a.score IS NOT NULL
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $2`, subjectID, limit)
			}
		} else {
			if uid != "" {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND a.score IS NOT NULL
                      AND NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_user_id=$1 AND b.blocked_user_id=a.student_user_id)
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $2`, uid, limit)
			} else {
				err = opts.DB.Select(&rows, `SELECT a.student_user_id, a.score::float AS score, a.test_id, t.subject_id
                    FROM test_attempts a
                    JOIN tests t ON t.id=a.test_id
                    WHERE a.status <> 'in_progress' AND t.visibility='public' AND a.score IS NOT NULL
                    ORDER BY a.score DESC, a.submitted_at DESC NULLS LAST LIMIT $1`, limit)
			}
		}
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// --- Moderation: Reports and Blocks ---
	app.Post("/v1/moderation/reports", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			TargetType string  `json:"targetType"`
			TargetID   string  `json:"targetId"`
			Reason     *string `json:"reason"`
			Details    *string `json:"details"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.TargetType) == "" || strings.TrimSpace(body.TargetID) == "" {
			return fiber.ErrBadRequest
		}
		// Relationship guard to prevent spam reports
		ttype := strings.ToLower(strings.TrimSpace(body.TargetType))
		allowed := false
		switch ttype {
		case "school":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND status='active')`, body.TargetID, uid)
			allowed = ok
		case "class":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM classes c
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE c.id=$1 AND (c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "subject":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM subjects sub
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE sub.id=$1 AND (c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "lesson":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM lessons l
                JOIN subjects sub ON sub.id=l.subject_id
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE l.id=$1 AND (c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "test":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM tests t
                LEFT JOIN subjects sub ON sub.id=t.subject_id
                LEFT JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$2
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$2 AND m.status='active'
                WHERE t.id=$1 AND (t.visibility='public' OR c.tutor_user_id=$2 OR e.id IS NOT NULL OR m.id IS NOT NULL)
            )`, body.TargetID, uid)
			allowed = ok
		case "user":
			var ok bool
			_ = opts.DB.Get(&ok, `SELECT EXISTS (
                SELECT 1 FROM (
                  SELECT c.tutor_user_id AS uid FROM classes c JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                  UNION
                  SELECT e2.student_user_id AS uid FROM class_enrollments e2 WHERE e2.class_id IN (SELECT e.class_id FROM class_enrollments e WHERE e.student_user_id=$1)
                  UNION
                  SELECT m2.user_id AS uid FROM school_members m1 JOIN school_members m2 ON m2.school_id=m1.school_id WHERE m1.user_id=$1 AND m1.status='active' AND m2.status='active'
                ) u WHERE u.uid=$2
            )`, uid, body.TargetID)
			allowed = ok
		default:
			allowed = false
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		if _, err := opts.DB.Exec(`INSERT INTO moderation_reports (reporter_user_id, target_type, target_id, reason, details) VALUES ($1,$2,$3,$4,$5)`, uid, strings.ToLower(body.TargetType), body.TargetID, body.Reason, body.Details); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/moderation/reports", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		status := c.Query("status", "open")
		type row struct {
			ID         string    `json:"id" db:"id"`
			Reporter   string    `json:"reporterUserId" db:"reporter_user_id"`
			TargetType string    `json:"targetType" db:"target_type"`
			TargetID   string    `json:"targetId" db:"target_id"`
			Reason     *string   `json:"reason" db:"reason"`
			Details    *string   `json:"details" db:"details"`
			Status     string    `json:"status" db:"status"`
			CreatedAt  time.Time `json:"createdAt" db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, reporter_user_id, target_type, target_id, reason, details, status, created_at FROM moderation_reports WHERE status=$1 ORDER BY created_at DESC LIMIT 200`, status); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/moderation/reports/:id/resolve", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		if !isPlatformAdmin(uid) {
			return fiber.ErrForbidden
		}
		rid := c.Params("id")
		var body struct {
			Status string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		st := strings.ToLower(strings.TrimSpace(body.Status))
		if st != "reviewed" && st != "dismissed" && st != "action_taken" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`UPDATE moderation_reports SET status=$1, reviewed_by_user_id=$2, reviewed_at=now() WHERE id=$3`, st, uid, rid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Get("/v1/users/blocks", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		type row struct {
			BlockedUserID string    `json:"blockedUserId" db:"blocked_user_id"`
			CreatedAt     time.Time `json:"createdAt" db:"created_at"`
		}
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT blocked_user_id, created_at FROM user_blocks WHERE blocker_user_id=$1 ORDER BY created_at DESC`, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/users/blocks", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		var body struct {
			BlockedUserID string `json:"blockedUserId"`
		}
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.BlockedUserID) == "" {
			return fiber.ErrBadRequest
		}
		if _, err := opts.DB.Exec(`INSERT INTO user_blocks (blocker_user_id, blocked_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, body.BlockedUserID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})
	app.Delete("/v1/users/blocks/:blockedUserId", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		bid := c.Params("blockedUserId")
		if _, err := opts.DB.Exec(`DELETE FROM user_blocks WHERE blocker_user_id=$1 AND blocked_user_id=$2`, uid, bid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	// --- Search (typeahead) ---
	app.Get("/v1/search", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		t := strings.ToLower(strings.TrimSpace(c.Query("type")))
		q := strings.ToLower(strings.TrimSpace(c.Query("q")))
		if t == "" {
			return fiber.ErrBadRequest
		}
		like := "%" + q + "%"
		switch t {
		case "user":
			// People search with privacy:
			// - Always include verified tutors (private_tutors.status='approved')
			// - Include "related" users only: classmates, class tutors, and members of the same schools
			// - Match against display name, email, uid, and school name(s) tied to the user (via membership or class enrollments)
			type row struct {
				UserID      string  `json:"userId" db:"user_id"`
				DisplayName *string `json:"displayName,omitempty" db:"display_name"`
			}
			rows := []row{}
			scope := strings.ToLower(strings.TrimSpace(c.Query("scope")))
			global := false
			if scope == "global" {
				var count int
				if err := opts.DB.Get(&count, `SELECT COUNT(*) FROM school_members WHERE user_id=$1 AND role='admin' AND status='active'`, uid); err == nil && count > 0 {
					global = true
				}
			}
			if global {
				if err := opts.DB.Select(&rows, `
                    SELECT up.user_id, up.display_name
                    FROM user_profiles up
                    WHERE ($1='' OR up.display_name ILIKE $2 OR COALESCE(up.email,'') ILIKE $2 OR up.user_id ILIKE $2)
                    ORDER BY up.display_name NULLS LAST, up.user_id
                    LIMIT 50`, q, like); err != nil {
					return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
				}
				return c.JSON(fiber.Map{"success": true, "data": rows})
			}
			if err := opts.DB.Select(&rows, `
                WITH related(uid) AS (
                  SELECT c.tutor_user_id FROM classes c JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                  UNION
                  SELECT e2.student_user_id FROM class_enrollments e2 WHERE e2.class_id IN (
                    SELECT e.class_id FROM class_enrollments e WHERE e.student_user_id=$1
                  )
                  UNION
                  SELECT m2.user_id FROM school_members m1
                    JOIN school_members m2 ON m2.school_id=m1.school_id
                  WHERE m1.user_id=$1 AND m1.status='active' AND m2.status='active'
                ),
                approved_tutors(uid) AS (
                  SELECT user_id FROM private_tutors WHERE status='approved'
                ),
                cand(uid) AS (
                  SELECT uid FROM related
                  UNION
                  SELECT uid FROM approved_tutors
                )
                SELECT c.uid AS user_id, up.display_name
                FROM cand c
                LEFT JOIN user_profiles up ON up.user_id=c.uid
                LEFT JOIN school_members sm ON sm.user_id=c.uid
                LEFT JOIN schools sch ON sch.id=sm.school_id
                LEFT JOIN class_enrollments e ON e.student_user_id=c.uid
                LEFT JOIN classes cl ON cl.id=e.class_id
                LEFT JOIN schools sch2 ON sch2.id=cl.school_id
                WHERE ($2='' OR up.display_name ILIKE $3 OR COALESCE(up.email,'') ILIKE $3 OR c.uid ILIKE $3 OR sch.name ILIKE $3 OR sch2.name ILIKE $3)
                GROUP BY c.uid, up.display_name
                LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "school":
			type row struct {
				ID   string `json:"id" db:"id"`
				Name string `json:"name" db:"name"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT s.id, s.name FROM schools s JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 WHERE ($2='' OR s.name ILIKE $3) LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "class":
			type row struct {
				ID    string `json:"id" db:"id"`
				Title string `json:"title" db:"title"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT c.id, c.title FROM classes c
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (c.tutor_user_id=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL) AND ($2='' OR c.title ILIKE $3)
                ORDER BY c.created_at DESC LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "subject":
			type row struct {
				ID    string `json:"id" db:"id"`
				Title string `json:"title" db:"title"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT sub.id, sub.title FROM subjects sub
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (c.tutor_user_id=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL) AND ($2='' OR sub.title ILIKE $3)
                ORDER BY sub.order_index, sub.title LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "lesson":
			type row struct {
				ID      string `json:"id" db:"id"`
				Title   string `json:"title" db:"title"`
				ClassID string `json:"classId" db:"class_id"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT l.id, l.title, c.id AS class_id FROM lessons l
                JOIN subjects sub ON sub.id=l.subject_id
                JOIN classes c ON c.id=sub.class_id
                LEFT JOIN class_enrollments e ON e.class_id=c.id AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=c.school_id
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (c.tutor_user_id=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL) AND ($2='' OR l.title ILIKE $3)
                ORDER BY l.order_index, l.title LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		case "test":
			type row struct {
				ID      string  `json:"id" db:"id"`
				Title   string  `json:"title" db:"title"`
				ClassID *string `json:"classId,omitempty" db:"class_id"`
			}
			rows := []row{}
			if err := opts.DB.Select(&rows, `SELECT t.id, t.title,
                    COALESCE(c.id, c2.id) AS class_id
                FROM tests t
                LEFT JOIN subjects sub ON sub.id=t.subject_id
                LEFT JOIN classes c ON c.id=sub.class_id
                LEFT JOIN lessons l ON l.id=t.lesson_id
                LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
                LEFT JOIN classes c2 ON c2.id=sub2.class_id
                LEFT JOIN class_enrollments e ON e.class_id=COALESCE(c.id, c2.id) AND e.student_user_id=$1
                LEFT JOIN schools s ON s.id=COALESCE(c.school_id, c2.school_id)
                LEFT JOIN school_members m ON m.school_id=s.id AND m.user_id=$1 AND m.status='active'
                WHERE (t.visibility='public' OR COALESCE(c.tutor_user_id, c2.tutor_user_id)=$1 OR e.id IS NOT NULL OR m.id IS NOT NULL)
                  AND ($2='' OR t.title ILIKE $3)
                ORDER BY t.created_at DESC LIMIT 20`, uid, q, like); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
			return c.JSON(fiber.Map{"success": true, "data": rows})
		default:
			return fiber.ErrBadRequest
		}
	})

	return app
}

func nullIfEmptyJSON(j json.RawMessage) any {
	if len(j) == 0 || string(j) == "null" {
		return nil
	}
	return j
}

func coalesceString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}
