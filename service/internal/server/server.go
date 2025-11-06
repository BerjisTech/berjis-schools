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
	"github.com/jung-kurt/gofpdf"
)
type Options struct {
	AllowedOrigins string
	CoreAPIBase    string
	DB             *sqlx.DB
}
func New(opts Options) *fiber.App {
    // Helper: evaluate certificate rules for a user in a class and issue if eligible
    evaluateCerts := func(classID, userID string) {
        if opts.DB == nil || classID == "" || userID == "" { return }
        var schoolID *string
        _ = opts.DB.Get(&schoolID, `SELECT school_id FROM classes WHERE id=$1`, classID)
        type rule struct { ID string `db:"id"`; Scope string `db:"scope"`; TemplateID string `db:"template_id"`; Conditions []byte `db:"conditions"` }
        rules := []rule{}
        if schoolID != nil && *schoolID != "" {
            _ = opts.DB.Select(&rules, `SELECT id, scope, template_id, COALESCE(conditions,'{}'::jsonb) AS conditions
                FROM certificate_rules WHERE enabled=true AND ((scope='class' AND class_id=$1) OR (scope='school' AND school_id=$2))`, classID, *schoolID)
        } else {
            _ = opts.DB.Select(&rules, `SELECT id, scope, template_id, COALESCE(conditions,'{}'::jsonb) AS conditions
                FROM certificate_rules WHERE enabled=true AND (scope='class' AND class_id=$1)`, classID)
        }
        if len(rules) == 0 { return }
        var max float64
        {
            type trow struct{ ID string `db:"id"` }
            tests := []trow{}
            _ = opts.DB.Select(&tests, `SELECT t.id FROM tests t
                LEFT JOIN subjects s ON s.id=t.subject_id
                LEFT JOIN lessons l ON l.id=t.lesson_id
                LEFT JOIN subjects s2 ON s2.id=l.subject_id
                LEFT JOIN classes c ON c.id=s.class_id
                LEFT JOIN classes c2 ON c2.id=s2.class_id
                WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'`, classID)
            for _, tr := range tests {
                var m float64
                _ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, tr.ID)
                max += m
            }
        }
        earned := 0.0
        if max > 0 {
            type arow struct{ TestID string `db:"test_id"`; Score *float64 `db:"score"`; Grading []byte `db:"grading"` }
            attempts := []arow{}
            _ = opts.DB.Select(&attempts, `SELECT ta.test_id, ta.score, COALESCE(ta.grading,'null'::jsonb) AS grading FROM test_attempts ta
               WHERE ta.student_user_id=$1 AND ta.test_id IN (
                    SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$2 OR c2.id=$2) AND t.status<>'deleted'
               )`, userID, classID)
            for _, a := range attempts {
                var m float64
                _ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, a.TestID)
                if len(a.Grading) > 0 && string(a.Grading) != "null" {
                    var g map[string]any
                    _ = json.Unmarshal(a.Grading, &g)
                    if v, ok := g["earned"].(float64); ok { earned += v; continue }
                }
                if a.Score != nil { earned += (*a.Score / 100.0) * m }
            }
        }
        percent := 0.0
        if max > 0 { percent = (earned / max) * 100.0 }
        var totalTests int
        _ = opts.DB.Get(&totalTests, `SELECT COUNT(*) FROM (
            SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted') q`, classID)
        var completedTests int
        _ = opts.DB.Get(&completedTests, `SELECT COUNT(*) FROM test_attempts WHERE student_user_id=$1 AND test_id IN (
            SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$2 OR c2.id=$2) AND t.status<>'deleted') AND status<>'in_progress'`, userID, classID)
        var totalLessons int
        _ = opts.DB.Get(&totalLessons, `SELECT COUNT(*) FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$1 AND l.status='active'`, classID)
        var lessonsCompleted int
        _ = opts.DB.Get(&lessonsCompleted, `SELECT COUNT(*) FROM lesson_progress WHERE student_user_id=$1 AND lesson_id IN (SELECT l.id FROM lessons l JOIN subjects s ON s.id=l.subject_id WHERE s.class_id=$2) AND status='completed'`, userID, classID)
        for _, r := range rules {
            var cond struct {
                MinPercent float64 `json:"minPercent"`
                RequireAllTestsCompleted bool `json:"requireAllTestsCompleted"`
                MinLessonsCompleted int `json:"minLessonsCompleted"`
            }
            _ = json.Unmarshal(r.Conditions, &cond)
            if cond.MinPercent > 0 && percent+1e-9 < cond.MinPercent { continue }
            if cond.RequireAllTestsCompleted && totalTests > 0 && completedTests < totalTests { continue }
            if cond.MinLessonsCompleted > 0 && lessonsCompleted < cond.MinLessonsCompleted { continue }
            var exists bool
            _ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM certificates WHERE template_id=$1 AND recipient_user_id=$2 AND class_id=$3 AND status='issued')`, r.TemplateID, userID, classID)
            if exists { continue }
            code := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))
            if len(code) > 12 { code = code[:12] }
            verifyURL := strings.TrimRight(opts.CoreAPIBase, "/")
            _, _ = opts.DB.Exec(`INSERT INTO certificates (template_id, recipient_user_id, class_id, data, code, issued_by_user_id, verify_url) VALUES ($1,$2,$3,'{}',$4,$5,$6)`, r.TemplateID, userID, classID, code, "system", verifyURL)
        }
    }
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
		// Basic type guard
		ct := strings.ToLower(strings.TrimSpace(fh.Header.Get("Content-Type")))
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		allowedExts := map[string]string{
			".pdf":  "application/pdf",
			".png":  "image/",
			".jpg":  "image/",
			".jpeg": "image/",
			".webp": "image/",
			".heic": "image/",
			".heif": "image/",
			".mp4":  "video/",
			".mov":  "video/",
			".m4v":  "video/",
			".webm": "video/",
		}
		allowedDisplay := "PDF, PNG, JPG, JPEG, WEBP, HEIC/HEIF, MP4, MOV, M4V, WebM"
		pattern, ok := allowedExts[ext]
		if !ok {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Unsupported file type. Allowed: " + allowedDisplay})
		}
		if ct != "" && ct != "application/octet-stream" && !strings.HasPrefix(ct, pattern) {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "File content type not allowed. Allowed: " + allowedDisplay})
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
		name := uuid.New().String() + ext
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
		ID           string    `json:"id" db:"id"`
		SchoolID     *string   `json:"schoolId,omitempty" db:"school_id"`
		TutorUserID  string    `json:"tutorUserId" db:"tutor_user_id"`
		Title        string    `json:"title" db:"title"`
		Description  *string   `json:"description,omitempty" db:"description"`
		Visibility   string    `json:"visibility" db:"visibility"`
		IsPaid       bool      `json:"isPaid" db:"is_paid"`
		PriceCents   int       `json:"priceCents" db:"price_cents"`
		StudentCount int       `json:"studentCount" db:"student_count"`
		SubjectCount int       `json:"subjectCount" db:"subject_count"`
		LessonCount  int       `json:"lessonCount" db:"lesson_count"`
		TestCount    int       `json:"testCount" db:"test_count"`
		Status       string    `json:"status" db:"status"`
		Enrolled     bool      `json:"isEnrolled" db:"is_enrolled"`
		CreatedAt    time.Time `json:"createdAt" db:"created_at"`
		UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
	}
	app.Get("/v1/classes", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		schoolID := c.Query("school_id")
		mine := strings.EqualFold(c.Query("mine"), "1") || strings.EqualFold(c.Query("mine"), "true")
		tutorFilter := c.Query("tutor_user_id")
		uid, _ := getUserID(c) // optional; if present include owned/private
		if mine && uid == "" {
			return fiber.ErrUnauthorized
		}
		rows := []class{}
		if schoolID != "" {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE (c.school_id=$1 OR c.tutor_user_id=$2 OR c.visibility='public') AND c.status='active'
                    ORDER BY c.created_at DESC`, schoolID, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else if mine && uid != "" {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE c.tutor_user_id=$1 AND c.status='active'
                    ORDER BY c.created_at DESC`, uid, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else if tutorFilter != "" {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE c.tutor_user_id=$1 AND c.status='active'
                    ORDER BY c.created_at DESC`, tutorFilter, uid); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		} else {
			if err := opts.DB.Select(&rows, `SELECT c.id, c.school_id, c.tutor_user_id, c.title, c.description, c.visibility, c.is_paid, c.price_cents,
                    COALESCE(stats.student_count,0) AS student_count, COALESCE(subj.subject_count,0) AS subject_count,
                    COALESCE(less.lesson_count,0) AS lesson_count, COALESCE(tests.test_count,0) AS test_count,
                    c.status,
                    CASE WHEN enr.student_user_id IS NOT NULL THEN TRUE ELSE FALSE END AS is_enrolled,
                    c.created_at, c.updated_at
                    FROM classes c
                    LEFT JOIN (
                        SELECT class_id, COUNT(*) FILTER (WHERE status='active')::int AS student_count
                        FROM class_enrollments
                        GROUP BY class_id
                    ) stats ON stats.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS subject_count FROM subjects WHERE status='active' GROUP BY class_id
                    ) subj ON subj.class_id=c.id
                    LEFT JOIN (
                        SELECT sub.class_id, COUNT(*)::int AS lesson_count
                        FROM lessons l
                        JOIN subjects sub ON sub.id=l.subject_id
                        WHERE l.status='active' AND sub.status='active'
                        GROUP BY sub.class_id
                    ) less ON less.class_id=c.id
                    LEFT JOIN (
                        SELECT class_id, COUNT(*)::int AS test_count FROM (
                            SELECT COALESCE(sub.class_id, sub2.class_id) AS class_id
                            FROM tests t
                            LEFT JOIN subjects sub ON sub.id=t.subject_id AND sub.status='active'
                            LEFT JOIN lessons l ON l.id=t.lesson_id AND l.status='active'
                            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id AND sub2.status='active'
                            WHERE t.status='active'
                        ) q GROUP BY class_id
                    ) tests ON tests.class_id=c.id
                    LEFT JOIN class_enrollments enr ON enr.class_id=c.id AND enr.student_user_id=$2
                    WHERE (c.visibility='public' OR c.tutor_user_id=$1) AND c.status='active'
                    ORDER BY c.created_at DESC`, uid, uid); err != nil {
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
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, tutor_user_id, title, description, visibility, is_paid, price_cents, status, created_at, updated_at`,
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
	app.Patch("/v1/classes/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		classID := c.Params("id")
		var existing struct {
			TutorUserID string  `db:"tutor_user_id"`
			SchoolID    *string `db:"school_id"`
		}
		if err := opts.DB.Get(&existing, `SELECT tutor_user_id, school_id FROM classes WHERE id=$1`, classID); err != nil {
			return fiber.ErrNotFound
		}
		if existing.TutorUserID != uid {
			allowed := false
			if existing.SchoolID != nil && *existing.SchoolID != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *existing.SchoolID, uid)
			}
			if !allowed {
				return fiber.ErrForbidden
			}
		}
		var body struct {
			Title       *string `json:"title"`
			Description *string `json:"description"`
			Visibility  *string `json:"visibility"`
			IsPaid      *bool   `json:"isPaid"`
			PriceCents  *int    `json:"priceCents"`
			Status      *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Title != nil {
			setClauses = append(setClauses, "title=$"+strconv.Itoa(len(args)+1))
			args = append(args, strings.TrimSpace(*body.Title))
		}
		if body.Description != nil {
			setClauses = append(setClauses, "description=$"+strconv.Itoa(len(args)+1))
			desc := strings.TrimSpace(*body.Description)
			if desc == "" {
				args = append(args, nil)
			} else {
				args = append(args, desc)
			}
		}
		if body.Visibility != nil {
			vis := strings.ToLower(strings.TrimSpace(*body.Visibility))
			if vis != "public" && vis != "private" && vis != "school" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "visibility=$"+strconv.Itoa(len(args)+1))
			args = append(args, vis)
		}
		if body.IsPaid != nil {
			setClauses = append(setClauses, "is_paid=$"+strconv.Itoa(len(args)+1))
			args = append(args, *body.IsPaid)
		}
		if body.PriceCents != nil {
			price := *body.PriceCents
			if price < 0 {
				price = 0
			}
			setClauses = append(setClauses, "price_cents=$"+strconv.Itoa(len(args)+1))
			args = append(args, price)
		}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		setClauses = append(setClauses, "updated_at=now()")
		args = append(args, classID)
		query := "UPDATE classes SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
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
		Status      string  `json:"status" db:"status"`
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
		if err := opts.DB.Select(&rows, `SELECT id, class_id, title, description, order_index, status FROM subjects WHERE class_id=$1 AND status='active' ORDER BY order_index ASC, title ASC`, classID); err != nil {
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
		if err := opts.DB.Get(&out, `INSERT INTO subjects (class_id, title, description, order_index) VALUES ($1,$2,$3,$4) RETURNING id, class_id, title, description, order_index, status`, in.ClassID, in.Title, in.Description, ord); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Patch("/v1/subjects/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		subjectID := c.Params("id")
		var meta struct {
			ClassID  string  `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&meta, `SELECT sub.class_id, cls.school_id
            FROM subjects sub
            JOIN classes cls ON cls.id=sub.class_id
            WHERE sub.id=$1`, subjectID); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsClassTutor(opts.DB, meta.ClassID, uid)
		if !allowed && meta.SchoolID != nil && *meta.SchoolID != "" {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, *meta.SchoolID, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Status *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, subjectID)
		query := "UPDATE subjects SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
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
		Status     string          `json:"status" db:"status"`
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
		if err := opts.DB.Select(&rows, `SELECT id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free, status FROM lessons WHERE subject_id=$1 AND status='active' ORDER BY order_index ASC, title ASC`, sid); err != nil {
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
		if err := opts.DB.Get(&out, `INSERT INTO lessons (subject_id, title, type, content, order_index, is_free) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free, status`, in.SubjectID, in.Title, in.Type, nullIfEmptyJSON(in.Content), ord, free); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Patch("/v1/lessons/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		lessonID := c.Params("id")
		var meta struct {
			ClassID  string  `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&meta, `SELECT cls.id AS class_id, cls.school_id
            FROM lessons l
            JOIN subjects sub ON sub.id=l.subject_id
            JOIN classes cls ON cls.id=sub.class_id
            WHERE l.id=$1`, lessonID); err != nil {
			return fiber.ErrNotFound
		}
		allowed, _ := auth.IsClassTutor(opts.DB, meta.ClassID, uid)
		if !allowed && meta.SchoolID != nil && *meta.SchoolID != "" {
			allowed, _ = auth.IsSchoolAdmin(opts.DB, *meta.SchoolID, uid)
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Status *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, lessonID)
		query := "UPDATE lessons SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
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
		Status      string    `json:"status" db:"status"`
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
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE lesson_id=$1 AND status='active' ORDER BY created_at DESC`, lid)
		} else if sid != "" {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE subject_id=$1 AND status='active' ORDER BY created_at DESC`, sid)
		} else {
			err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE status='active' ORDER BY created_at DESC`)
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
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at`, in.SchoolID, in.SubjectID, in.LessonID, in.Title, in.Description, vis, uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true, "data": out})
	})
	app.Patch("/v1/tests/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		testID := c.Params("id")
		var meta struct {
			Status   string  `db:"status"`
			ClassID  *string `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		if err := opts.DB.Get(&meta, `SELECT t.status, COALESCE(c.id, c2.id) AS class_id, COALESCE(c.school_id, c2.school_id) AS school_id
			FROM tests t
			LEFT JOIN subjects sub ON sub.id=t.subject_id
			LEFT JOIN classes c ON c.id=sub.class_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
			LEFT JOIN classes c2 ON c2.id=sub2.class_id
			WHERE t.id=$1`, testID); err != nil {
			return fiber.ErrNotFound
		}
		allowed := false
		if meta.ClassID != nil && *meta.ClassID != "" {
			allowed, _ = auth.IsClassTutor(opts.DB, *meta.ClassID, uid)
			if !allowed && meta.SchoolID != nil && *meta.SchoolID != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *meta.SchoolID, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		var body struct {
			Status *string `json:"status"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.ErrBadRequest
		}
		setClauses := []string{}
		args := []any{}
		if body.Status != nil {
			status := strings.ToLower(strings.TrimSpace(*body.Status))
			if status != "active" && status != "archived" && status != "deleted" {
				return fiber.ErrBadRequest
			}
			setClauses = append(setClauses, "status=$"+strconv.Itoa(len(args)+1))
			args = append(args, status)
		}
		if len(setClauses) == 0 {
			return c.JSON(fiber.Map{"success": true})
		}
		args = append(args, testID)
		query := "UPDATE tests SET " + strings.Join(setClauses, ", ") + " WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(query, args...); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
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
		if err := opts.DB.Get(&t, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, status, created_by_user_id, created_at FROM tests WHERE id=$1`, id); err != nil {
			return fiber.ErrNotFound
		}
		if t.Status == "deleted" {
			return fiber.ErrNotFound
		}
		// Permission check similar to search visibility for tests; also resolve classId and canEdit
		var link struct {
			ClassID  *string `db:"class_id"`
			SchoolID *string `db:"school_id"`
		}
		_ = opts.DB.Get(&link, `SELECT COALESCE(c.id, c2.id) AS class_id, COALESCE(c.school_id, c2.school_id) AS school_id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
		classID := link.ClassID
		schoolID := link.SchoolID
		allow := t.Visibility == "public"
		canEdit := false
		if classID != nil && *classID != "" {
			var tutorID string
			_ = opts.DB.Get(&tutorID, `SELECT tutor_user_id FROM classes WHERE id=$1`, classID)
			if tutorID == uid {
				allow = true
				canEdit = true
			}
			if !allow {
				var exists bool
				_ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2)`, classID, uid)
				if exists {
					allow = true
				}
			}
			if schoolID != nil && *schoolID != "" {
				if !allow {
					allow, _ = auth.IsSchoolMember(opts.DB, *schoolID, uid)
				}
				if !canEdit {
					canEdit, _ = auth.IsSchoolAdmin(opts.DB, *schoolID, uid)
				}
			}
		}
		if canEdit {
			allow = true
		}
		if t.Status == "archived" && !canEdit {
			return fiber.ErrForbidden
		}
		if !allow {
			return fiber.ErrForbidden
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
	// List attempts for a test (tutor/school admin only)
	app.Get("/v1/tests/:id/attempts", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		tid := c.Params("id")
		// Resolve related class for permission checks
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
			LEFT JOIN subjects sub ON sub.id=t.subject_id
			LEFT JOIN classes c ON c.id=sub.class_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
			LEFT JOIN classes c2 ON c2.id=sub2.class_id
			WHERE t.id=$1 LIMIT 1`, tid)
		allow := false
		if classID != nil && *classID != "" {
			allow, _ = auth.IsClassTutor(opts.DB, *classID, uid)
			if !allow {
				var sid *string
				_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
				if sid != nil && *sid != "" { allow, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) }
			}
		}
		if !allow { return fiber.ErrForbidden }
		status := strings.TrimSpace(strings.ToLower(c.Query("status")))
		type row struct {
			ID        string          `json:"id" db:"id"`
			StudentID string          `json:"studentUserId" db:"student_user_id"`
			Score     *float64        `json:"score" db:"score"`
			Status    string          `json:"status" db:"status"`
			Submitted *time.Time      `json:"submittedAt" db:"submitted_at"`
			Responses json.RawMessage `json:"responses" db:"responses"`
			Grading   json.RawMessage `json:"grading" db:"grading"`
		}
		rows := []row{}
		if status == "" || status == "any" {
			if err := opts.DB.Select(&rows, `SELECT id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading FROM test_attempts WHERE test_id=$1 ORDER BY submitted_at DESC NULLS LAST, id DESC LIMIT 500`, tid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		} else {
			if err := opts.DB.Select(&rows, `SELECT id, student_user_id, score, status, submitted_at, COALESCE(responses,'null'::jsonb) AS responses, COALESCE(grading,'null'::jsonb) AS grading FROM test_attempts WHERE test_id=$1 AND status=$2 ORDER BY submitted_at DESC NULLS LAST, id DESC LIMIT 500`, tid, status); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Manually grade an attempt (tutor/school admin only)
	app.Patch("/v1/tests/:id/attempts/:attemptId/grade", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		tid := c.Params("id")
		aid := c.Params("attemptId")
		// Permission same as list attempts
		var classID *string
		_ = opts.DB.Get(&classID, `SELECT c.id FROM tests t
			LEFT JOIN subjects sub ON sub.id=t.subject_id
			LEFT JOIN classes c ON c.id=sub.class_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
			LEFT JOIN classes c2 ON c2.id=sub2.class_id
			WHERE t.id=$1 LIMIT 1`, tid)
		allow := false
		if classID != nil && *classID != "" {
			allow, _ = auth.IsClassTutor(opts.DB, *classID, uid)
			if !allow {
				var sid *string
				_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
				if sid != nil && *sid != "" { allow, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) }
			}
		}
		if !allow { return fiber.ErrForbidden }
		var body struct { Score *float64 `json:"score"`; Grading json.RawMessage `json:"grading"`; Status *string `json:"status"` }
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		// Default status to graded
		st := "graded"
		if body.Status != nil {
			s := strings.ToLower(strings.TrimSpace(*body.Status))
			if s != "graded" && s != "submitted" && s != "returned" { return fiber.ErrBadRequest }
			st = s
		}
		// Update
		if _, err := opts.DB.Exec(`UPDATE test_attempts SET grading=COALESCE($1, grading), score=$2, status=$3, submitted_at=COALESCE(submitted_at, now()) WHERE id=$4 AND test_id=$5`, nullIfEmptyJSON(body.Grading), body.Score, st, aid, tid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
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
		// Trigger automation
        var link struct { ClassID *string `db:"class_id"` }
        _ = opts.DB.Get(&link, `SELECT COALESCE(c.id, c2.id) AS class_id FROM tests t
            LEFT JOIN subjects sub ON sub.id=t.subject_id
            LEFT JOIN classes c ON c.id=sub.class_id
            LEFT JOIN lessons l ON l.id=t.lesson_id
            LEFT JOIN subjects sub2 ON sub2.id=l.subject_id
            LEFT JOIN classes c2 ON c2.id=sub2.class_id
            WHERE t.id=$1 LIMIT 1`, id)
        if link.ClassID != nil && *link.ClassID != "" {
            evaluateCerts(*link.ClassID, uid)
        }
            evaluateCerts(*link.ClassID, uid)
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
	// --- Gradebook ---
	// Class gradebook: tutor/school admin only
	app.Get("/v1/classes/:id/gradebook", func(c *fiber.Ctx) error {
		if opts.DB == nil {
			return fiber.ErrInternalServerError
		}
		uid, err := getUserID(c)
		if err != nil {
			return fiber.ErrUnauthorized
		}
		classID := c.Params("id")
		// Permissions
		allowed, _ := auth.IsClassTutor(opts.DB, classID, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
			if sid != nil && *sid != "" {
				allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid)
			}
		}
		if !allowed {
			return fiber.ErrForbidden
		}
		// Students in class
		type studentRow struct { UserID string `db:"student_user_id"`; DisplayName *string `db:"display_name"` }
		students := []studentRow{}
		if err := opts.DB.Select(&students, `SELECT e.student_user_id, up.display_name
			FROM class_enrollments e LEFT JOIN user_profiles up ON up.user_id=e.student_user_id
			WHERE e.class_id=$1 AND e.status='active' ORDER BY up.display_name NULLS LAST, e.student_user_id`, classID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Tests linked to class via subjects/lessons
		type testRow struct { ID string `db:"id"`; Title string `db:"title"`; CreatedAt time.Time `db:"created_at"` }
		tests := []testRow{}
		if err := opts.DB.Select(&tests, `
			SELECT t.id, t.title, t.created_at FROM tests t
			LEFT JOIN subjects s ON s.id=t.subject_id
			LEFT JOIN lessons l ON l.id=t.lesson_id
			LEFT JOIN subjects s2 ON s2.id=l.subject_id
			LEFT JOIN classes c ON c.id=s.class_id
			LEFT JOIN classes c2 ON c2.id=s2.class_id
			WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'
			ORDER BY t.created_at ASC`, classID); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		// Max points per test
		maxByTest := map[string]float64{}
		for _, tr := range tests {
			var max float64
			_ = opts.DB.Get(&max, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, tr.ID)
			maxByTest[tr.ID] = max
		}
		// Attempts for all students across tests
		attempts := []struct {
			TestID  string          `db:"test_id"`
			UserID  string          `db:"student_user_id"`
			Score   *float64        `db:"score"`
			Status  string          `db:"status"`
			At      *time.Time      `db:"submitted_at"`
			Grading json.RawMessage `db:"grading"`
		}{}
		if len(tests) > 0 && len(students) > 0 {
			testIDs := make([]string, 0, len(tests))
			for _, t := range tests { testIDs = append(testIDs, t.ID) }
			studentIDs := make([]string, 0, len(students))
			for _, s := range students { studentIDs = append(studentIDs, s.UserID) }
			q, args, _ := sqlx.In(`SELECT test_id, student_user_id, score, status, submitted_at, grading FROM test_attempts WHERE test_id IN (?) AND student_user_id IN (?)`, testIDs, studentIDs)
			q = opts.DB.Rebind(q)
			if err := opts.DB.Select(&attempts, q, args...); err != nil {
				return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
			}
		}
		// Assemble map student->test->grade
		 type grade struct {
			Score       *float64   `json:"score"`
			Earned      *float64   `json:"earned"`
			Max         float64    `json:"max"`
			Status      string     `json:"status"`
			SubmittedAt *time.Time `json:"submittedAt"`
		}
		grades := map[string]map[string]grade{}
		for _, s := range students {
			grades[s.UserID] = map[string]grade{}
		}
		for _, a := range attempts {
			max := maxByTest[a.TestID]
			var earned *float64
			if len(a.Grading) > 0 && string(a.Grading) != "null" {
				var g map[string]any
				_ = json.Unmarshal(a.Grading, &g)
				if v, ok := g["earned"].(float64); ok { earned = &v }
				// if grading.max missing, fallback to schema max
			}
			if earned == nil && a.Score != nil && max > 0 {
				v := (*a.Score / 100.0) * max
				earned = &v
			}
			if _, ok := grades[a.UserID]; !ok { grades[a.UserID] = map[string]grade{} }
			grades[a.UserID][a.TestID] = grade{ Score: a.Score, Earned: earned, Max: max, Status: a.Status, SubmittedAt: a.At }
		}
		// Compute totals per student
		rows := []fiber.Map{}
		for _, s := range students {
			totalEarned := 0.0
			totalMax := 0.0
			gmap := grades[s.UserID]
			for _, tr := range tests {
				m := maxByTest[tr.ID]
				totalMax += m
				if g, ok := gmap[tr.ID]; ok && g.Earned != nil { totalEarned += *g.Earned }
			}
			var percent *float64
			if totalMax > 0 {
				p := (totalEarned / totalMax) * 100.0
				percent = &p
			}
			name := s.UserID
			if s.DisplayName != nil && strings.TrimSpace(*s.DisplayName) != "" { name = *s.DisplayName }
			rows = append(rows, fiber.Map{ "userId": s.UserID, "name": name, "earned": totalEarned, "max": totalMax, "percent": percent, "grades": gmap })
		}
		// tests summary
		testSumm := []fiber.Map{}
		for _, tr := range tests { testSumm = append(testSumm, fiber.Map{"id": tr.ID, "title": tr.Title, "max": maxByTest[tr.ID]}) }
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"tests": testSumm, "students": rows}})
	})
	// CSV export
	app.Get("/v1/classes/:id/gradebook.csv", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=gradebook.csv")
		// Build CSV from the same data as JSON endpoint
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		classID := c.Params("id")
		allowed, _ := auth.IsClassTutor(opts.DB, classID, uid)
		if !allowed {
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
			if sid != nil && *sid != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) }
		}
		if !allowed { return fiber.ErrForbidden }
		// fetch json pieces
		type TR struct { ID, Title string; Max float64 }
		tests := []TR{}
		_ = opts.DB.Select(&tests, `SELECT t.id, t.title, COALESCE((SELECT SUM(points) FROM test_questions WHERE test_id=t.id),0) AS max
			FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted' ORDER BY t.created_at ASC`, classID)
		students := []struct{ UserID, Name string }{}
		_ = opts.DB.Select(&students, `SELECT e.student_user_id AS user_id, COALESCE(NULLIF(TRIM(up.display_name),''), e.student_user_id) AS name FROM class_enrollments e LEFT JOIN user_profiles up ON up.user_id=e.student_user_id WHERE e.class_id=$1 AND e.status='active' ORDER BY name`, classID)
		// attempts map
		attempts := []struct{ TestID, UserID string; Score *float64; Grading json.RawMessage }{}
		if len(tests) > 0 && len(students) > 0 {
			tids := make([]string,0,len(tests)); for _, t := range tests { tids = append(tids, t.ID) }
			uids := make([]string,0,len(students)); for _, s := range students { uids = append(uids, s.UserID) }
			q, args, _ := sqlx.In(`SELECT test_id, student_user_id, score, grading FROM test_attempts WHERE test_id IN (?) AND student_user_id IN (?)`, tids, uids)
			q = opts.DB.Rebind(q)
			_ = opts.DB.Select(&attempts, q, args...)
		}
		// build map for quick lookup
		am := map[string]map[string]struct{Score *float64; Earned *float64}{ }
		for _, a := range attempts {
			if am[a.UserID] == nil { am[a.UserID] = map[string]struct{Score *float64; Earned *float64}{} }
			var earned *float64
			if len(a.Grading) > 0 && string(a.Grading) != "null" {
				var g map[string]any; _ = json.Unmarshal(a.Grading, &g); if v, ok := g["earned"].(float64); ok { earned = &v }
			}
			am[a.UserID][a.TestID] = struct{Score *float64; Earned *float64}{ a.Score, earned }
		}
		// write CSV
		var b strings.Builder
		b.WriteString("Student")
		for _, t := range tests { b.WriteString("," + strings.ReplaceAll(t.Title, ",", " ")) }
		b.WriteString(",Total %\n")
		for _, s := range students {
			b.WriteString(s.Name)
			totalEarned := 0.0
			totalMax := 0.0
			for _, t := range tests {
				var cell string
				if a, ok := am[s.UserID][t.ID]; ok {
					if a.Score != nil {
						cell = fmt.Sprintf("%.2f", *a.Score)
					} else if a.Earned != nil && t.Max > 0 {
						cell = fmt.Sprintf("%.2f", (*a.Earned/t.Max)*100.0)
					}
					if a.Earned != nil { totalEarned += *a.Earned }
				}
				totalMax += t.Max
				if cell == "" { cell = "" }
				b.WriteString("," + cell)
			}
			var pct string
			if totalMax > 0 { pct = fmt.Sprintf("%.2f", (totalEarned/totalMax)*100.0) }
			b.WriteString("," + pct + "\n")
		}
		return c.SendString(b.String())
	})
	// --- Transcripts ---
	// Helper: permission to view user transcript
	canViewUser := func(viewerID, targetID, schoolID string) bool {
		if viewerID == targetID { return true }
		// guardian
		var ok bool
		_ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, viewerID, targetID)
		if ok { return true }
		// platform admin
		if isPlatformAdmin(viewerID) { return true }
		// school admin scoped to schoolID if provided
		if strings.TrimSpace(schoolID) != "" {
			ok, _ = auth.IsSchoolAdmin(opts.DB, schoolID, viewerID)
			if ok { return true }
		}
		return false
	}
	// JSON transcript for a user (overall or filtered by school)
	app.Get("/v1/transcripts/:userId", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		viewer, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		target := c.Params("userId")
		if target == "me" { target = viewer }
		sid := strings.TrimSpace(c.Query("school_id"))
		if !canViewUser(viewer, target, sid) { return fiber.ErrForbidden }
		// Classes for target (optionally by school)
		type classRow struct { ID, Title string; SchoolID *string }
		classes := []classRow{}
		if sid == "" {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title, c.school_id FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' ORDER BY c.title`, target)
		} else {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title, c.school_id FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' AND c.school_id=$2 ORDER BY c.title`, target, sid)
		}
		// For each class, compute earned/max similarly to gradebook but for single user
		out := []fiber.Map{}
		for _, cr := range classes {
			// tests under class
			tests := []struct{ ID, Title string }{}
			_ = opts.DB.Select(&tests, `SELECT t.id, t.title FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted' ORDER BY t.created_at`, cr.ID)
			maxBy := map[string]float64{}
			for _, t := range tests { var m float64; _ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, t.ID); maxBy[t.ID] = m }
			attempts := []struct{ TestID string; Score *float64; Grading json.RawMessage }{}
			if len(tests) > 0 {
				tids := make([]string,0,len(tests)); for _, t := range tests { tids = append(tids, t.ID) }
				q, args, _ := sqlx.In(`SELECT test_id, score, grading FROM test_attempts WHERE student_user_id=$1 AND test_id IN (?)`, target, tids)
				q = opts.DB.Rebind(q)
				_ = opts.DB.Select(&attempts, q, args...)
			}
			earned := 0.0; max := 0.0
			for _, t := range tests { max += maxBy[t.ID] }
			for _, a := range attempts {
				if len(a.Grading) > 0 && string(a.Grading) != "null" {
					var g map[string]any; _ = json.Unmarshal(a.Grading, &g)
					if v, ok := g["earned"].(float64); ok { earned += v }
				} else if a.Score != nil {
					if mx := maxBy[a.TestID]; mx > 0 { earned += (*a.Score/100.0)*mx }
				}
			}
			var percent *float64
			if max > 0 { p := (earned/max)*100.0; percent = &p }
			out = append(out, fiber.Map{"classId": cr.ID, "title": cr.Title, "earned": earned, "max": max, "percent": percent})
		}
		// overall summary
		totalE, totalM := 0.0, 0.0
		for _, r := range out { totalE += r["earned"].(float64); totalM += r["max"].(float64) }
		var overall *float64
		if totalM > 0 { v := (totalE/totalM)*100.0; overall = &v }
		// student display
		var name string
		_ = opts.DB.Get(&name, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, target)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"userId": target, "name": name, "overallPercent": overall, "classes": out }})
	})
	// PDF transcript
	app.Get("/v1/transcripts/:userId.pdf", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		viewer, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		target := c.Params("userId")
		if target == "me" { target = viewer }
		sid := strings.TrimSpace(c.Query("school_id"))
		if !canViewUser(viewer, target, sid) { return fiber.ErrForbidden }
		// reuse JSON handler by querying DB directly (same as above, condensed)
		var name string
		_ = opts.DB.Get(&name, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, target)
		classes := []struct{ ID, Title string }{}
		if sid == "" {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' ORDER BY c.title`, target)
		} else {
			_ = opts.DB.Select(&classes, `SELECT c.id, c.title FROM classes c JOIN class_enrollments e ON e.class_id=c.id WHERE e.student_user_id=$1 AND e.status='active' AND c.school_id=$2 ORDER BY c.title`, target, sid)
		}
		rows := []struct{ Title string; Percent *float64 }{}
		for _, cr := range classes {
			// compute class percent
			tests := []struct{ ID string }{}
			_ = opts.DB.Select(&tests, `SELECT t.id FROM tests t LEFT JOIN subjects s ON s.id=t.subject_id LEFT JOIN lessons l ON l.id=t.lesson_id LEFT JOIN subjects s2 ON s2.id=l.subject_id LEFT JOIN classes c ON c.id=s.class_id LEFT JOIN classes c2 ON c2.id=s2.class_id WHERE (c.id=$1 OR c2.id=$1) AND t.status<>'deleted'`, cr.ID)
			mx := 0.0; earned := 0.0
			for _, t := range tests { var m float64; _ = opts.DB.Get(&m, `SELECT COALESCE(SUM(points),0) FROM test_questions WHERE test_id=$1`, t.ID); mx += m; var sc *float64; var gr json.RawMessage; _ = opts.DB.Get(&sc, `SELECT score FROM test_attempts WHERE student_user_id=$1 AND test_id=$2`, target, t.ID); _ = opts.DB.Get(&gr, `SELECT grading FROM test_attempts WHERE student_user_id=$1 AND test_id=$2`, target, t.ID); if len(gr) > 0 && string(gr) != "null" { var g map[string]any; _ = json.Unmarshal(gr, &g); if v, ok := g["earned"].(float64); ok { earned += v } } else if sc != nil && m > 0 { earned += (*sc/100.0)*m } }
			var pct *float64; if mx > 0 { v := (earned/mx)*100.0; pct = &v }
			rows = append(rows, struct{Title string; Percent *float64}{cr.Title, pct})
		}
		// render PDF
		pdf := gofpdf.New("P", "mm", "A4", "")
		pdf.SetTitle("Transcript", false)
		pdf.AddPage()
		pdf.SetFont("Helvetica", "B", 16)
		pdf.Cell(0, 10, "Academic Transcript")
		pdf.Ln(12)
		pdf.SetFont("Helvetica", "", 12)
		pdf.Cell(0, 8, fmt.Sprintf("Student: %s (%s)", name, target))
		pdf.Ln(10)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(120, 8, "Class", "B", 0, "L", false, 0, "")
		pdf.CellFormat(40, 8, "Percent", "B", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 11)
		for _, r := range rows {
			pdf.CellFormat(120, 7, r.Title, "", 0, "L", false, 0, "")
			var pctStr string
			if r.Percent != nil { pctStr = fmt.Sprintf("%.2f%%", *r.Percent) } else { pctStr = "—" }
			pdf.CellFormat(40, 7, pctStr, "", 1, "L", false, 0, "")
		}
		var buf bytes.Buffer
		if err := pdf.Output(&buf); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		c.Set("Content-Type", "application/pdf")
		c.Set("Content-Disposition", "inline; filename=transcript.pdf")
		return c.Send(buf.Bytes())
	})
	// --- Certificates ---
	genCode := func() string {
		r := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))
		if len(r) > 12 { r = r[:12] }
		return r
	}
	// Create a certificate template
	app.Post("/v1/certificates/templates", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		var body struct {
			Scope    string          `json:"scope"`   // 'platform' or 'school'
			SchoolID string          `json:"schoolId"`
			Name     string          `json:"name"`
			Body     json.RawMessage `json:"body"`
			Style    json.RawMessage `json:"style"`
		}
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		scope := strings.ToLower(strings.TrimSpace(body.Scope))
		if scope != "platform" && scope != "school" { return fiber.ErrBadRequest }
		if scope == "platform" {
			if !isPlatformAdmin(uid) { return fiber.ErrForbidden }
		}
		var schoolID *string
		if scope == "school" {
			s := strings.TrimSpace(body.SchoolID)
			if s == "" { return fiber.ErrBadRequest }
			ok, _ := auth.IsSchoolAdmin(opts.DB, s, uid); if !ok { return fiber.ErrForbidden }
			schoolID = &s
		}
		var out struct{ ID string `json:"id"`; Name string `json:"name"`; Scope string `json:"scope"`; SchoolID *string `json:"schoolId"` }
		if err := opts.DB.Get(&out, `INSERT INTO certificate_templates (scope, school_id, name, body, style, created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6)
			RETURNING id, name, scope, school_id`, scope, schoolID, strings.TrimSpace(body.Name), nullIfEmptyJSON(body.Body), nullIfEmptyJSON(body.Style), uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// List templates (platform admin sees platform; school admin sees their school)
	app.Get("/v1/certificates/templates", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		scope := strings.TrimSpace(strings.ToLower(c.Query("scope")))
		sid := strings.TrimSpace(c.Query("school_id"))
		type row struct{ ID, Name, Scope string; SchoolID *string `db:"school_id"` }
		rows := []row{}
		if scope == "platform" || (scope == "" && isPlatformAdmin(uid)) {
			if !isPlatformAdmin(uid) { return fiber.ErrForbidden }
			_ = opts.DB.Select(&rows, `SELECT id, name, scope, school_id FROM certificate_templates WHERE scope='platform' AND status='active' ORDER BY created_at DESC`)
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		if sid == "" { return fiber.ErrBadRequest }
		ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid); if !ok { return fiber.ErrForbidden }
		_ = opts.DB.Select(&rows, `SELECT id, name, scope, school_id FROM certificate_templates WHERE scope='school' AND school_id=$1 AND status='active' ORDER BY created_at DESC`, sid)
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Issue a certificate
	app.Post("/v1/certificates/issue", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		var body struct {
			TemplateID string          `json:"templateId"`
			Recipient  string          `json:"recipientUserId"`
			ClassID    string          `json:"classId"`
			Data       json.RawMessage `json:"data"`
		}
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		if strings.TrimSpace(body.TemplateID) == "" || strings.TrimSpace(body.Recipient) == "" { return fiber.ErrBadRequest }
		// Load template and check permission
		var tpl struct{ ID, Scope string; SchoolID *string `db:"school_id"` }
		if err := opts.DB.Get(&tpl, `SELECT id, scope, school_id FROM certificate_templates WHERE id=$1 AND status='active'`, body.TemplateID); err != nil {
			return fiber.ErrBadRequest
		}
		if tpl.Scope == "platform" {
			if !isPlatformAdmin(uid) { return fiber.ErrForbidden }
		} else {
			if tpl.SchoolID == nil || *tpl.SchoolID == "" { return fiber.ErrBadRequest }
			ok, _ := auth.IsSchoolAdmin(opts.DB, *tpl.SchoolID, uid)
			if !ok {
				// allow class tutor if class belongs to same school and provided
				if strings.TrimSpace(body.ClassID) == "" { return fiber.ErrForbidden }
				ok, _ = auth.IsClassTutor(opts.DB, body.ClassID, uid)
				if !ok {
					return fiber.ErrForbidden
				}
			}
		}
		code := genCode()
		verifyURL := strings.TrimRight(opts.CoreAPIBase, "/") // could be public URL; reuse core base as placeholder
		var out struct{ ID, Code string }
		if err := opts.DB.Get(&out, `INSERT INTO certificates (template_id, recipient_user_id, class_id, data, code, issued_by_user_id, verify_url)
			VALUES ($1,$2, NULLIF($3,''), $4, $5, $6, $7) RETURNING id, code`, body.TemplateID, body.Recipient, body.ClassID, nullIfEmptyJSON(body.Data), code, uid, verifyURL); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// Revoke a certificate
	app.Post("/v1/certificates/:id/revoke", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		id := c.Params("id")
		// owner permission: platform or school admin for the template scope
		var tpl struct{ Scope string; SchoolID *string `db:"school_id"` }
		if err := opts.DB.Get(&tpl, `SELECT t.scope, t.school_id FROM certificates ce JOIN certificate_templates t ON t.id=ce.template_id WHERE ce.id=$1`, id); err != nil { return fiber.ErrBadRequest }
		if tpl.Scope == "platform" {
			if !isPlatformAdmin(uid) { return fiber.ErrForbidden }
		} else {
			if tpl.SchoolID == nil || *tpl.SchoolID == "" { return fiber.ErrBadRequest }
			ok, _ := auth.IsSchoolAdmin(opts.DB, *tpl.SchoolID, uid); if !ok { return fiber.ErrForbidden }
		}
		_, err = opts.DB.Exec(`UPDATE certificates SET status='revoked', revoked_at=now() WHERE id=$1 AND status<>'revoked'`, id)
		if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Verify endpoint by code
	app.Get("/v1/certificates/verify", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		code := strings.TrimSpace(c.Query("code"))
		if code == "" { return fiber.ErrBadRequest }
		var row struct { ID, TemplateName string; Status string; Recipient string; IssuedAt time.Time }
		if err := opts.DB.Get(&row, `SELECT ce.id, tp.name AS template_name, ce.status, ce.recipient_user_id AS recipient, ce.issued_at
			FROM certificates ce JOIN certificate_templates tp ON tp.id=ce.template_id WHERE ce.code=$1`, code); err != nil {
			return fiber.ErrNotFound
		}
		var display string
		_ = opts.DB.Get(&display, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, row.Recipient)
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"id": row.ID, "template": row.TemplateName, "status": row.Status, "recipient": fiber.Map{"userId": row.Recipient, "name": display}, "issuedAt": row.IssuedAt}})
	})
	// Certificate PDF by ID (public if you have the code param)
	app.Get("/v1/certificates/:id.pdf", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		id := c.Params("id")
		code := strings.TrimSpace(c.Query("code"))
		var row struct { ID, Code, TemplateName string; Status string; Recipient string; IssuedAt time.Time }
		if err := opts.DB.Get(&row, `SELECT ce.id, ce.code, tp.name AS template_name, ce.status, ce.recipient_user_id AS recipient, ce.issued_at FROM certificates ce JOIN certificate_templates tp ON tp.id=ce.template_id WHERE ce.id=$1`, id); err != nil { return fiber.ErrNotFound }
		// If not provided correct code and not authenticated authorized viewer, disallow when revoked
		if code != row.Code {
			// require auth and ownership
			uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
			if uid != row.Recipient && !isPlatformAdmin(uid) { return fiber.ErrForbidden }
		}
		var name string
		_ = opts.DB.Get(&name, `SELECT COALESCE(NULLIF(TRIM(display_name),''), $1) FROM user_profiles WHERE user_id=$1`, row.Recipient)
		pdf := gofpdf.New("L", "mm", "A4", "")
		pdf.AddPage()
		pdf.SetFont("Helvetica", "B", 28)
		pdf.Cell(0, 18, row.TemplateName)
		pdf.Ln(20)
		pdf.SetFont("Helvetica", "", 16)
		pdf.Cell(0, 10, fmt.Sprintf("Awarded to %s", name))
		pdf.Ln(12)
		pdf.Cell(0, 8, fmt.Sprintf("Issued: %s", row.IssuedAt.Format("2006-01-02")))
		pdf.Ln(10)
		pdf.SetFont("Helvetica", "I", 11)
		pdf.Cell(0, 7, fmt.Sprintf("Certificate Code: %s", row.Code))
		pdf.Ln(8)
		pdf.Cell(0, 7, "Verify at: /v1/certificates/verify?code="+row.Code)
		var buf bytes.Buffer
		if err := pdf.Output(&buf); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		c.Set("Content-Type", "application/pdf")
		c.Set("Content-Disposition", "inline; filename=certificate.pdf")
		return c.Send(buf.Bytes())
	})

    // --- Discussions & Announcements ---
    // Check if user is class member (enrolled) or tutor/admin
    isClassMember := func(classID, userID string) bool {
        if classID == "" || userID == "" { return false }
        var ok bool
        _ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2 AND status='active')`, classID, userID)
        if ok { return true }
        ok, _ = auth.IsClassTutor(opts.DB, classID, userID)
        if ok { return true }
        var sid *string
        _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID)
        if sid != nil && *sid != "" { ok, _ = auth.IsSchoolAdmin(opts.DB, *sid, userID); if ok { return true } }
        return false
    }

    // Discussions
    app.Get("/v1/classes/:id/discussions", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        cid := c.Params("id")
        if !isClassMember(cid, uid) { return fiber.ErrForbidden }
        type row struct{ ID, Title, CreatedBy string; CreatedAt time.Time `db:"created_at"` }
        rows := []row{}
        if err := opts.DB.Select(&rows, `SELECT id, title, created_by_user_id AS created_by, created_at FROM class_discussions WHERE class_id=$1 ORDER BY created_at DESC LIMIT 200`, cid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    app.Post("/v1/classes/:id/discussions", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        cid := c.Params("id")
        if !isClassMember(cid, uid) { return fiber.ErrForbidden }
        var body struct{ Title string `json:"title"` }
        if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title)=="" { return fiber.ErrBadRequest }
        var out struct{ ID string `json:"id"` }
        if err := opts.DB.Get(&out, `INSERT INTO class_discussions (class_id, title, created_by_user_id) VALUES ($1,$2,$3) RETURNING id`, cid, strings.TrimSpace(body.Title), uid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
    })
    app.Get("/v1/discussions/:id", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        did := c.Params("id")
        var cid string
        if err := opts.DB.Get(&cid, `SELECT class_id FROM class_discussions WHERE id=$1`, did); err != nil { return fiber.ErrNotFound }
        if !isClassMember(cid, uid) { return fiber.ErrForbidden }
        var head struct{ ID, Title string; CreatedBy string; CreatedAt time.Time }
        _ = opts.DB.Get(&head, `SELECT id, title, created_by_user_id AS created_by, created_at FROM class_discussions WHERE id=$1`, did)
        type post struct{ ID, UserID, Body string; CreatedAt time.Time; Attachments json.RawMessage }
        posts := []post{}
        _ = opts.DB.Select(&posts, `SELECT id, user_id, body, created_at, COALESCE(attachments,'null'::jsonb) AS attachments FROM class_posts WHERE discussion_id=$1 ORDER BY created_at ASC`, did)
        // moderation: class tutor or school admin
        canMod, _ := auth.IsClassTutor(opts.DB, cid, uid)
        if !canMod {
            var sid *string
            _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
            if sid != nil && *sid != "" { canMod, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) }
        }
        return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"discussion": head, "posts": posts, "canModerate": canMod}})
    })
    app.Post("/v1/discussions/:id/posts", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        did := c.Params("id")
        var cid string
        if err := opts.DB.Get(&cid, `SELECT class_id FROM class_discussions WHERE id=$1`, did); err != nil { return fiber.ErrNotFound }
        if !isClassMember(cid, uid) { return fiber.ErrForbidden }
        var body struct{ Body string `json:"body"`; Attachments json.RawMessage `json:"attachments"` }
        if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Body)=="" { return fiber.ErrBadRequest }
        var out struct{ ID string `json:"id"` }
        if err := opts.DB.Get(&out, `INSERT INTO class_posts (discussion_id, user_id, body, attachments) VALUES ($1,$2,$3,$4) RETURNING id`, did, uid, strings.TrimSpace(body.Body), nullIfEmptyJSON(body.Attachments)); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
    })

    // Announcements
    app.Get("/v1/classes/:id/announcements", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        cid := c.Params("id")
        if !isClassMember(cid, uid) { return fiber.ErrForbidden }
        type row struct { ID, Title, Body, CreatedBy string; Pinned bool; PublishedAt time.Time }
        rows := []row{}
        if err := opts.DB.Select(&rows, `SELECT id, title, COALESCE(body,'') AS body, created_by_user_id AS created_by, pinned, published_at FROM class_announcements WHERE class_id=$1 ORDER BY pinned DESC, published_at DESC LIMIT 200`, cid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    app.Post("/v1/classes/:id/announcements", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        cid := c.Params("id")
        allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
        if !allowed { var sid *string; _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid); if sid != nil && *sid != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) } }
        if !allowed { return fiber.ErrForbidden }
        var body struct{ Title string `json:"title"`; Body *string `json:"body"`; Pinned *bool `json:"pinned"` }
        if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title)=="" { return fiber.ErrBadRequest }
        pinned := false; if body.Pinned != nil { pinned = *body.Pinned }
        var out struct{ ID string `json:"id"` }
        if err := opts.DB.Get(&out, `INSERT INTO class_announcements (class_id, title, body, pinned, created_by_user_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`, cid, strings.TrimSpace(body.Title), body.Body, pinned, uid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        studentIDs := []string{}
        _ = opts.DB.Select(&studentIDs, `SELECT student_user_id FROM class_enrollments WHERE class_id=$1 AND status='active'`, cid)
        for _, sid := range studentIDs {
            nb, _ := json.Marshal(fiber.Map{"type": "class_announcement", "classId": cid, "announcementId": out.ID})
            _, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, sid, "class_announcement", nb)
        }
        return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
    })

	// Certificate Rules
	app.Post("/v1/certificates/rules", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		var body struct {
			Scope string `json:"scope"`
			SchoolID string `json:"schoolId"`
			ClassID string `json:"classId"`
			TemplateID string `json:"templateId"`
			Enabled bool `json:"enabled"`
			Conditions json.RawMessage `json:"conditions"`
		}
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		scope := strings.ToLower(strings.TrimSpace(body.Scope))
		if scope != "class" && scope != "school" { return fiber.ErrBadRequest }
		if strings.TrimSpace(body.TemplateID) == "" { return fiber.ErrBadRequest }
		var sid *string
		var cid *string
		if scope == "school" {
			if strings.TrimSpace(body.SchoolID) == "" { return fiber.ErrBadRequest }
			s := strings.TrimSpace(body.SchoolID); sid=&s
			ok, _ := auth.IsSchoolAdmin(opts.DB, s, uid); if !ok { return fiber.ErrForbidden }
		} else {
			if strings.TrimSpace(body.ClassID) == "" { return fiber.ErrBadRequest }
			s := strings.TrimSpace(body.ClassID); cid=&s
			// allow class tutor or school admin where applicable
			var tutor string; _ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, s)
			allowed := tutor == uid
			if !allowed { var sch *string; _ = opts.DB.Get(&sch, `SELECT school_id FROM classes WHERE id=$1`, s); if sch != nil && *sch != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sch, uid) } }
			if !allowed { return fiber.ErrForbidden }
		}
		var out struct{ ID string `json:"id"` }
		if err := opts.DB.Get(&out, `INSERT INTO certificate_rules (scope, school_id, class_id, template_id, enabled, conditions, created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, scope, sid, cid, body.TemplateID, body.Enabled, nullIfEmptyJSON(body.Conditions), uid); err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})

	app.Get("/v1/certificates/rules", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		sid := strings.TrimSpace(c.Query("school_id"))
		cid := strings.TrimSpace(c.Query("class_id"))
		type row struct{ ID, Scope, TemplateID string; Enabled bool; SchoolID *string `db:"school_id"`; ClassID *string `db:"class_id"`; Conditions json.RawMessage }
		rows := []row{}
		if cid != "" {
			var tutor string; _ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, cid)
			allowed := tutor == uid
			if !allowed { var sch *string; _ = opts.DB.Get(&sch, `SELECT school_id FROM classes WHERE id=$1`, cid); if sch != nil && *sch != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sch, uid) } }
			if !allowed { return fiber.ErrForbidden }
			_ = opts.DB.Select(&rows, `SELECT id, scope, template_id, enabled, school_id, class_id, COALESCE(conditions,'{}'::jsonb) AS conditions FROM certificate_rules WHERE class_id=$1 ORDER BY created_at DESC`, cid)
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		if sid != "" {
			ok, _ := auth.IsSchoolAdmin(opts.DB, sid, uid); if !ok { return fiber.ErrForbidden }
			_ = opts.DB.Select(&rows, `SELECT id, scope, template_id, enabled, school_id, class_id, COALESCE(conditions,'{}'::jsonb) AS conditions FROM certificate_rules WHERE school_id=$1 ORDER BY created_at DESC`, sid)
			return c.JSON(fiber.Map{"success": true, "data": rows})
		}
		return fiber.ErrBadRequest
	})

	app.Patch("/v1/certificates/rules/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		id := c.Params("id")
		var cur struct{ Scope string; SchoolID *string `db:"school_id"`; ClassID *string `db:"class_id"` }
		if err := opts.DB.Get(&cur, `SELECT scope, school_id, class_id FROM certificate_rules WHERE id=$1`, id); err != nil { return fiber.ErrNotFound }
		// permission
		if cur.Scope == "school" {
			if cur.SchoolID == nil || *cur.SchoolID == "" { return fiber.ErrBadRequest }
			ok, _ := auth.IsSchoolAdmin(opts.DB, *cur.SchoolID, uid); if !ok { return fiber.ErrForbidden }
		} else {
			if cur.ClassID == nil || *cur.ClassID == "" { return fiber.ErrBadRequest }
			var tutor string; _ = opts.DB.Get(&tutor, `SELECT tutor_user_id FROM classes WHERE id=$1`, *cur.ClassID)
			allowed := tutor == uid
			if !allowed { var sch *string; _ = opts.DB.Get(&sch, `SELECT school_id FROM classes WHERE id=$1`, *cur.ClassID); if sch != nil && *sch != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sch, uid) } }
			if !allowed { return fiber.ErrForbidden }
		}
		var body struct{ Enabled *bool `json:"enabled"`; Conditions json.RawMessage `json:"conditions"` }
		_ = c.BodyParser(&body)
		sets := []string{}
		args := []any{}
		if body.Enabled != nil { sets = append(sets, "enabled=$"+strconv.Itoa(len(args)+1)); args = append(args, *body.Enabled) }
		if body.Conditions != nil { sets = append(sets, "conditions=$"+strconv.Itoa(len(args)+1)); args = append(args, nullIfEmptyJSON(body.Conditions)) }
		if len(sets) == 0 { return c.JSON(fiber.Map{"success": true}) }
		args = append(args, id)
		q := "UPDATE certificate_rules SET " + strings.Join(sets, ", ") + ", updated_at=now() WHERE id=$" + strconv.Itoa(len(args))
		if _, err := opts.DB.Exec(q, args...); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// --- Affiliation Management (Primary + Transfer) ---
	// Get current user's affiliations (optionally by role)
	app.Get("/v1/affiliations", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		role := strings.TrimSpace(strings.ToLower(c.Query("role")))
		type row struct {
			SchoolID  string `json:"schoolId" db:"school_id"`
			Role      string `json:"role" db:"role"`
			Status    string `json:"status" db:"status"`
			IsPrimary bool   `json:"isPrimary" db:"is_primary"`
		}
		rows := []row{}
		var q string
		if role == "" || role == "any" {
			q = `SELECT school_id, role, status, COALESCE(is_primary,false) AS is_primary FROM school_members WHERE user_id=$1 ORDER BY role, is_primary DESC, created_at DESC`
			if err := opts.DB.Select(&rows, q, uid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		} else {
			q = `SELECT school_id, role, status, COALESCE(is_primary,false) AS is_primary FROM school_members WHERE user_id=$1 AND role=$2 ORDER BY is_primary DESC, created_at DESC`
			if err := opts.DB.Select(&rows, q, uid, role); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		}
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Set current user's primary affiliation for a role
	app.Post("/v1/affiliations/primary", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		var body struct { Role string `json:"role"`; SchoolID string `json:"schoolId"` }
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		role := strings.ToLower(strings.TrimSpace(body.Role))
		if role == "" { return fiber.ErrBadRequest }
		sid := strings.TrimSpace(body.SchoolID); if sid == "" { return fiber.ErrBadRequest }
		// Ensure membership exists and is active
		var exists bool
		if err := opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role=$3 AND status='active')`, sid, uid, role); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		if !exists { return c.Status(400).JSON(fiber.Map{"success": false, "message": "Active membership not found"}) }
		// Clear others then set primary
		_, _ = opts.DB.Exec(`UPDATE school_members SET is_primary=false WHERE user_id=$1 AND role=$2`, uid, role)
		if _, err := opts.DB.Exec(`UPDATE school_members SET is_primary=true WHERE school_id=$1 AND user_id=$2 AND role=$3`, sid, uid, role); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Transfer current user's affiliation for a role from one school to another
	app.Post("/v1/affiliations/transfer", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		var body struct { Role string `json:"role"`; FromSchoolID string `json:"fromSchoolId"`; ToSchoolID string `json:"toSchoolId"`; MakePrimary *bool `json:"makePrimary"` }
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		role := strings.ToLower(strings.TrimSpace(body.Role))
		fromID := strings.TrimSpace(body.FromSchoolID)
		toID := strings.TrimSpace(body.ToSchoolID)
		if role == "" || fromID == "" || toID == "" || fromID == toID { return fiber.ErrBadRequest }
		// Verify existing membership
		var ok bool
		if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role=$3 AND status='active')`, fromID, uid, role); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		if !ok { return c.Status(400).JSON(fiber.Map{"success": false, "message": "Source membership not found"}) }
		// Ensure target membership exists (upsert active)
		if _, err := opts.DB.Exec(`INSERT INTO school_members (school_id, user_id, role, status, is_primary) VALUES ($1,$2,$3,'active',false)
			ON CONFLICT (school_id, user_id, role) DO UPDATE SET status='active'`, toID, uid, role); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		// Optionally deactivate old membership
		_, _ = opts.DB.Exec(`UPDATE school_members SET status='inactive', is_primary=false WHERE school_id=$1 AND user_id=$2 AND role=$3`, fromID, uid, role)
		if body.MakePrimary != nil && *body.MakePrimary {
			_, _ = opts.DB.Exec(`UPDATE school_members SET is_primary=false WHERE user_id=$1 AND role=$2`, uid, role)
			_, _ = opts.DB.Exec(`UPDATE school_members SET is_primary=true WHERE school_id=$1 AND user_id=$2 AND role=$3`, toID, uid, role)
		}
		return c.JSON(fiber.Map{"success": true})
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
			// Enqueue notification for owner
			notif := fiber.Map{"type": "school_application_approved", "applicationId": id, "schoolId": sid}
			nb, _ := json.Marshal(notif)
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, owner, "school_application_approved", nb)
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
			// Notify owner of rejection
			notif := fiber.Map{"type": "school_application_rejected", "applicationId": id, "reason": body.Reason}
			nb, _ := json.Marshal(notif)
			// Fetch owner id for recipient
			var owner string
			_ = opts.DB.Get(&owner, `SELECT owner_user_id FROM school_applications WHERE id=$1`, id)
			if owner != "" {
				_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, owner, "school_application_rejected", nb)
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
			// Notify tutor
			nb, _ := json.Marshal(fiber.Map{"type": "tutor_application_approved", "userId": tid})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, tid, "tutor_application_approved", nb)
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
			nb, _ := json.Marshal(fiber.Map{"type": "tutor_application_rejected", "userId": tid, "reason": body.Reason})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, tid, "tutor_application_rejected", nb)
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

	// --- Direct Messages (1:1) ---
	// Start or get a thread with another user in a class context
	app.Post("/v1/messages/start", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		var body struct { UserID string `json:"userId"`; ClassID string `json:"classId"` }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.UserID)=="" || strings.TrimSpace(body.ClassID)=="" { return fiber.ErrBadRequest }
		target := strings.TrimSpace(body.UserID)
		cid := strings.TrimSpace(body.ClassID)
		// Permission: either tutor<->student (enrolled) or guardian<->tutor for that class
		allowed := false
		// tutor <-> student
		isTutor, _ := auth.IsClassTutor(opts.DB, cid, uid)
		var enrolled bool
		_ = opts.DB.Get(&enrolled, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2 AND status='active')`, cid, target)
		if isTutor && enrolled { allowed = true }
		// reverse (student to tutor)
		if !allowed {
			isTutorTarget, _ := auth.IsClassTutor(opts.DB, cid, target)
			_ = opts.DB.Get(&enrolled, `SELECT EXISTS (SELECT 1 FROM class_enrollments WHERE class_id=$1 AND student_user_id=$2 AND status='active')`, cid, uid)
			if isTutorTarget && enrolled { allowed = true }
		}
		// guardian <-> tutor
		if !allowed {
			isTutorTarget, _ := auth.IsClassTutor(opts.DB, cid, target)
			if isTutorTarget {
				var child string
				_ = opts.DB.Get(&child, `SELECT e.student_user_id FROM class_enrollments e WHERE e.class_id=$1 AND EXISTS (SELECT 1 FROM guardians_children g WHERE g.guardian_user_id=$2 AND g.child_user_id=e.student_user_id) LIMIT 1`, cid, uid)
				if strings.TrimSpace(child) != "" { allowed = true }
			}
		}
		if !allowed { return fiber.ErrForbidden }
		// find existing thread for pair+class
		var tid string
		err = opts.DB.Get(&tid, `SELECT dt.id FROM direct_threads dt
			JOIN direct_participants p1 ON p1.thread_id=dt.id AND p1.user_id=$1
			JOIN direct_participants p2 ON p2.thread_id=dt.id AND p2.user_id=$2
			WHERE dt.class_id=$3 LIMIT 1`, uid, target, cid)
		if err != nil || tid == "" {
			if err := opts.DB.Get(&tid, `INSERT INTO direct_threads (class_id) VALUES ($1) RETURNING id`, cid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
			_, _ = opts.DB.Exec(`INSERT INTO direct_participants (thread_id, user_id) VALUES ($1,$2),($1,$3) ON CONFLICT DO NOTHING`, tid, uid, target)
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": fiber.Map{"threadId": tid}})
	})

	// List threads for current user
	app.Get("/v1/messages/threads", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		type row struct { ID string `db:"id"`; ClassID string `db:"class_id"`; Other string `db:"other_user"`; LastMsg *string `db:"last_msg"`; LastAt *time.Time `db:"last_at"` }
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT dt.id, dt.class_id,
			(SELECT user_id FROM direct_participants WHERE thread_id=dt.id AND user_id<>$1 LIMIT 1) AS other_user,
			(SELECT m.body FROM direct_messages m WHERE m.thread_id=dt.id ORDER BY m.created_at DESC LIMIT 1) AS last_msg,
			(SELECT m.created_at FROM direct_messages m WHERE m.thread_id=dt.id ORDER BY m.created_at DESC LIMIT 1) AS last_at
			FROM direct_threads dt WHERE EXISTS (SELECT 1 FROM direct_participants p WHERE p.thread_id=dt.id AND p.user_id=$1)
			ORDER BY last_at DESC NULLS LAST, dt.created_at DESC LIMIT 200`, uid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Get messages for a thread
	app.Get("/v1/messages/threads/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has { return fiber.ErrForbidden }
		type msg struct { ID, Sender, Body string; Attachments json.RawMessage; CreatedAt time.Time }
		rows := []msg{}
		if err := opts.DB.Select(&rows, `SELECT id, sender_user_id AS sender, COALESCE(body,'') AS body, COALESCE(attachments,'null'::jsonb) AS attachments, created_at FROM direct_messages WHERE thread_id=$1 ORDER BY created_at ASC LIMIT 500`, id); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})

	// Send a message
	app.Post("/v1/messages/threads/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has { return fiber.ErrForbidden }
		var body struct { Body *string `json:"body"`; Attachments json.RawMessage `json:"attachments"` }
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		var out struct{ ID string `json:"id"` }
		if err := opts.DB.Get(&out, `INSERT INTO direct_messages (thread_id, sender_user_id, body, attachments) VALUES ($1,$2,$3,$4) RETURNING id`, id, uid, body.Body, nullIfEmptyJSON(body.Attachments)); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		var other string
		_ = opts.DB.Get(&other, `SELECT user_id FROM direct_participants WHERE thread_id=$1 AND user_id<>$2 LIMIT 1`, id, uid)
		if other != "" {
			nb, _ := json.Marshal(fiber.Map{"type": "direct_message", "threadId": id, "messageId": out.ID})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, other, "direct_message", nb)
		}
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})


	// --- 1:1 Video Calls ---
	roomCode := func() string { return strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))[:12] }
	providerBase := func() string { b := strings.TrimSpace(os.Getenv("LIVE_PROVIDER_BASE")); if b=="" { b = "https://meet.jit.si" }; return strings.TrimRight(b, "/") }

	// Start a call in a message thread
	app.Post("/v1/messages/threads/:id/call/start", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has { return fiber.ErrForbidden }
		// resolve class and other participant
		var classID string
		if err := opts.DB.Get(&classID, `SELECT class_id FROM direct_threads WHERE id=$1`, id); err != nil { return fiber.ErrBadRequest }
		var other string
		_ = opts.DB.Get(&other, `SELECT user_id FROM direct_participants WHERE thread_id=$1 AND user_id<>$2 LIMIT 1`, id, uid)
		// determine tutor id if any
		var tutorID string
		if ok, _ := auth.IsClassTutor(opts.DB, classID, uid); ok { tutorID = uid } else if ok, _ := auth.IsClassTutor(opts.DB, classID, other); ok { tutorID = other }
		code := roomCode()
		url := providerBase()+"/"+code
		// create live room
		_, err = opts.DB.Exec(`INSERT INTO live_rooms (title, purpose, media, interaction, teacher_only, class_id, tutor_user_id, scheduled_at, room_code, provider_url)
			VALUES ($1,'tutor','video','interactive',false,$2, NULLIF($3,''), now(), $4, $5)`, "1:1 Call", classID, tutorID, code, url)
		if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		// notify other
		if other != "" {
			nb, _ := json.Marshal(fiber.Map{"type": "call_invite", "threadId": id, "roomCode": code, "url": url})
			_, _ = opts.DB.Exec(`INSERT INTO notifications_queue (recipient_user_id, ntype, payload) VALUES ($1,$2,$3)`, other, "call_invite", nb)
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": url, "roomCode": code}})
	})

	// Get latest active call URL for a thread
	app.Get("/v1/messages/threads/:id/call", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		id := c.Params("id")
		var has bool
		_ = opts.DB.Get(&has, `SELECT EXISTS (SELECT 1 FROM direct_participants WHERE thread_id=$1 AND user_id=$2)`, id, uid)
		if !has { return fiber.ErrForbidden }
		var classID string
		if err := opts.DB.Get(&classID, `SELECT class_id FROM direct_threads WHERE id=$1`, id); err != nil { return fiber.ErrBadRequest }
		var url string
		if err := opts.DB.Get(&url, `SELECT provider_url FROM live_rooms WHERE class_id=$1 AND purpose='tutor' AND ended_at IS NULL AND scheduled_at > now()- interval '3 hours' ORDER BY scheduled_at DESC LIMIT 1`, classID); err != nil || strings.TrimSpace(url)=="" {
			return c.JSON(fiber.Map{"success": true, "data": nil})
		}
		return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"url": url}})
	})


	// --- Study Groups ---
	isGroupMod := func(gid, uid string) bool {
		if gid == "" || uid == "" { return false }
		// owner/moderator
		var role string
		_ = opts.DB.Get(&role, `SELECT role FROM study_group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid)
		if role == "owner" || role == "moderator" { return true }
		// class tutor/admin
		var cid string
		_ = opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid)
		if cid != "" {
			ok, _ := auth.IsClassTutor(opts.DB, cid, uid)
			if ok { return true }
			var sid *string
			_ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid)
			if sid != nil && *sid != "" { ok, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid); if ok { return true } }
		}
		return false
	}
	// List groups for a class (members only)
	app.Get("/v1/classes/:id/groups", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		cid := c.Params("id")
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		type row struct{ ID, Title, CreatedBy string; CreatedAt time.Time }
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT id, title, created_by_user_id AS created_by, created_at FROM study_groups WHERE class_id=$1 ORDER BY created_at DESC`, cid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Create group (any class member)
	app.Post("/v1/classes/:id/groups", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		cid := c.Params("id")
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		var body struct{ Title string `json:"title"` }
		if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.Title)=="" { return fiber.ErrBadRequest }
		var gid string
		if err := opts.DB.Get(&gid, `INSERT INTO study_groups (class_id, title, created_by_user_id) VALUES ($1,$2,$3) RETURNING id`, cid, strings.TrimSpace(body.Title), uid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		_, _ = opts.DB.Exec(`INSERT INTO study_group_members (group_id, user_id, role) VALUES ($1,$2,'owner') ON CONFLICT DO NOTHING`, gid, uid)
		return c.Status(201).JSON(fiber.Map{"success": true, "data": map[string]string{"id": gid}})
	})
	// Join/Leave
	app.Post("/v1/groups/:id/join", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil { return fiber.ErrNotFound }
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		_, err = opts.DB.Exec(`INSERT INTO study_group_members (group_id, user_id) VALUES ($1,$2) ON CONFLICT (group_id, user_id) DO UPDATE SET status='active'`, gid, uid)
		if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/groups/:id/leave", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id")
		_, err = opts.DB.Exec(`UPDATE study_group_members SET status='left' WHERE group_id=$1 AND user_id=$2`, gid, uid)
		if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Messages
	app.Get("/v1/groups/:id/messages", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil { return fiber.ErrNotFound }
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		type msg struct{ ID, Sender, Body string; Attachments json.RawMessage; CreatedAt time.Time }
		rows := []msg{}
		if err := opts.DB.Select(&rows, `SELECT id, sender_user_id AS sender, COALESCE(body,'') AS body, COALESCE(attachments,'null'::jsonb) AS attachments, created_at FROM study_group_messages WHERE group_id=$1 ORDER BY created_at ASC LIMIT 500`, gid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	app.Post("/v1/groups/:id/messages", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil { return fiber.ErrNotFound }
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		var body struct{ Body *string `json:"body"`; Attachments json.RawMessage `json:"attachments"` }
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		var out struct{ ID string `json:"id"` }
		if err := opts.DB.Get(&out, `INSERT INTO study_group_messages (group_id, sender_user_id, body, attachments) VALUES ($1,$2,$3,$4) RETURNING id`, gid, uid, body.Body, nullIfEmptyJSON(body.Attachments)); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
	})
	// List members
	app.Get("/v1/groups/:id/members", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil { return fiber.ErrNotFound }
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		type row struct{ UserID, Role, Status string }
		rows := []row{}
		if err := opts.DB.Select(&rows, `SELECT user_id, role, status FROM study_group_members WHERE group_id=$1 ORDER BY role DESC, user_id`, gid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true, "data": rows})
	})
	// Set role (owner/mod/class tutor/admin)
	app.Post("/v1/groups/:id/members/:userId/role", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		actor, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id"); target := c.Params("userId")
		if !isGroupMod(gid, actor) { return fiber.ErrForbidden }
		var body struct{ Role string `json:"role"` }
		if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
		role := strings.ToLower(strings.TrimSpace(body.Role))
		if role != "member" && role != "moderator" && role != "owner" { return fiber.ErrBadRequest }
		if _, err := opts.DB.Exec(`UPDATE study_group_members SET role=$1 WHERE group_id=$2 AND user_id=$3`, role, gid, target); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Ban/unban
	app.Post("/v1/groups/:id/members/:userId/ban", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		actor, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id"); target := c.Params("userId")
		if !isGroupMod(gid, actor) { return fiber.ErrForbidden }
		if _, err := opts.DB.Exec(`UPDATE study_group_members SET status='banned' WHERE group_id=$1 AND user_id=$2`, gid, target); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	app.Post("/v1/groups/:id/members/:userId/unban", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		actor, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id"); target := c.Params("userId")
		if !isGroupMod(gid, actor) { return fiber.ErrForbidden }
		if _, err := opts.DB.Exec(`UPDATE study_group_members SET status='active' WHERE group_id=$1 AND user_id=$2`, gid, target); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Delete a group message
	app.Delete("/v1/groups/messages/:msgId", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		actor, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		mid := c.Params("msgId")
		var gid string
		if err := opts.DB.Get(&gid, `SELECT group_id FROM study_group_messages WHERE id=$1`, mid); err != nil { return fiber.ErrNotFound }
		if !isGroupMod(gid, actor) { return fiber.ErrForbidden }
		if _, err := opts.DB.Exec(`DELETE FROM study_group_messages WHERE id=$1`, mid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Delete a discussion post (class tutor/admin)
	app.Delete("/v1/discussions/posts/:id", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		pid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT d.class_id FROM class_posts p JOIN class_discussions d ON d.id=p.discussion_id WHERE p.id=$1`, pid); err != nil { return fiber.ErrNotFound }
		allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
		if !allowed { var sid *string; _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid); if sid != nil && *sid != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) } }
		if !allowed { return fiber.ErrForbidden }
		if _, err := opts.DB.Exec(`DELETE FROM class_posts WHERE id=$1`, pid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true})
	})
	// Start a group call
	app.Post("/v1/groups/:id/call/start", func(c *fiber.Ctx) error {
		if opts.DB == nil { return fiber.ErrInternalServerError }
		uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
		gid := c.Params("id")
		var cid string
		if err := opts.DB.Get(&cid, `SELECT class_id FROM study_groups WHERE id=$1`, gid); err != nil { return fiber.ErrNotFound }
		if !isClassMember(cid, uid) { return fiber.ErrForbidden }
		code := strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", ""))[:12]
		url := strings.TrimRight(providerBase(), "/")+"/"+code
		_, err = opts.DB.Exec(`INSERT INTO live_rooms (title, purpose, media, interaction, teacher_only, class_id, scheduled_at, room_code, provider_url)
			VALUES ($1,'study','video','interactive',false,$2, now(), $3, $4)`, "Study Group", cid, code, url)
		if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
		return c.JSON(fiber.Map{"success": true, "data": map[string]string{"url": url, "roomCode": code}})
	})


    // --- Appointments / Office Hours ---
    // List slots for a class (members only)
    app.Get("/v1/classes/:id/appointments/slots", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        cid := c.Params("id")
        if !isClassMember(cid, uid) { return fiber.ErrForbidden }
        type row struct{ ID, TutorUserID string; StartAt, EndAt time.Time; Capacity int; LocationURL *string; Notes *string }
        rows := []row{}
        if err := opts.DB.Select(&rows, `SELECT id, tutor_user_id, start_at, end_at, capacity, location_url, notes FROM appointment_slots WHERE class_id=$1 AND end_at >= now() ORDER BY start_at ASC LIMIT 200`, cid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })

    // Create a slot (tutor or school admin)
    app.Post("/v1/classes/:id/appointments/slots", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        cid := c.Params("id")
        allowed, _ := auth.IsClassTutor(opts.DB, cid, uid)
        if !allowed { var sid *string; _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, cid); if sid != nil && *sid != "" { allowed, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) } }
        if !allowed { return fiber.ErrForbidden }
        var body struct { StartAt string `json:"startAt"`; EndAt string `json:"endAt"`; Capacity *int `json:"capacity"`; LocationURL *string `json:"locationUrl"`; Notes *string `json:"notes"` }
        if err := c.BodyParser(&body); err != nil { return fiber.ErrBadRequest }
        if strings.TrimSpace(body.StartAt)=="" || strings.TrimSpace(body.EndAt)=="" { return fiber.ErrBadRequest }
        st, err1 := time.Parse(time.RFC3339, body.StartAt); en, err2 := time.Parse(time.RFC3339, body.EndAt)
        if err1 != nil || err2 != nil || !en.After(st) { return fiber.ErrBadRequest }
        cap := 1
        if body.Capacity != nil && *body.Capacity > 0 && *body.Capacity <= 50 { cap = *body.Capacity }
        var out struct{ ID string `json:"id"` }
        if err := opts.DB.Get(&out, `INSERT INTO appointment_slots (class_id, tutor_user_id, start_at, end_at, capacity, location_url, notes) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, cid, uid, st, en, cap, body.LocationURL, body.Notes); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
    })

    // Book a slot (student or guardian for child)
    app.Post("/v1/appointments/slots/:slotId/book", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        sid := c.Params("slotId")
        var row struct{ ClassID string; StartAt, EndAt time.Time; Capacity int }
        if err := opts.DB.Get(&row, `SELECT class_id, start_at, end_at, capacity FROM appointment_slots WHERE id=$1`, sid); err != nil { return fiber.ErrNotFound }
        if !isClassMember(row.ClassID, uid) { return fiber.ErrForbidden }
        var body struct { ForUserID *string `json:"forUserId"` }
        _ = c.BodyParser(&body)
        // If guardian booking for child, verify linkage
        if body.ForUserID != nil && strings.TrimSpace(*body.ForUserID) != "" {
            var ok bool
            _ = opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, *body.ForUserID)
            if !ok { return fiber.ErrForbidden }
        }
        // Check capacity
        var count int
        _ = opts.DB.Get(&count, `SELECT COUNT(*) FROM appointment_bookings WHERE slot_id=$1 AND status='booked'`, sid)
        if count >= row.Capacity { return c.Status(400).JSON(fiber.Map{"success": false, "message": "Slot full"}) }
        // Prevent duplicate
        var exists bool
        _ = opts.DB.Get(&exists, `SELECT EXISTS (SELECT 1 FROM appointment_bookings WHERE slot_id=$1 AND booker_user_id=$2 AND status='booked')`, sid, uid)
        if exists { return c.Status(400).JSON(fiber.Map{"success": false, "message": "Already booked"}) }
        // Create booking
        var out struct{ ID string `json:"id"` }
        var forUID any
        if body.ForUserID != nil {
            v := strings.TrimSpace(*body.ForUserID)
            if v != "" { forUID = v } else { forUID = nil }
        } else { forUID = nil }
        if err := opts.DB.Get(&out, `INSERT INTO appointment_bookings (slot_id, booker_user_id, for_user_id) VALUES ($1,$2,$3) RETURNING id`, sid, uid, forUID); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.Status(201).JSON(fiber.Map{"success": true, "data": out})
    })

    // Cancel booking (booker or tutor)
    app.Post("/v1/appointments/bookings/:id/cancel", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        bid := c.Params("id")
        var rec struct{ SlotID string; Booker string; ClassID string }
        if err := opts.DB.Get(&rec, `SELECT b.slot_id, b.booker_user_id, s.class_id FROM appointment_bookings b JOIN appointment_slots s ON s.id=b.slot_id WHERE b.id=$1`, bid); err != nil { return fiber.ErrNotFound }
        allowed := (rec.Booker == uid)
        if !allowed { allowed, _ = auth.IsClassTutor(opts.DB, rec.ClassID, uid) }
        if !allowed { return fiber.ErrForbidden }
        if _, err := opts.DB.Exec(`UPDATE appointment_bookings SET status='cancelled', cancelled_at=now() WHERE id=$1 AND status='booked'`, bid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true})
    })

    // List my bookings
    app.Get("/v1/appointments/my", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        type row struct{ ID, SlotID, ClassID string; StartAt, EndAt time.Time; Status string }
        rows := []row{}
        if err := opts.DB.Select(&rows, `SELECT b.id, b.slot_id, s.class_id, s.start_at, s.end_at, b.status FROM appointment_bookings b JOIN appointment_slots s ON s.id=b.slot_id WHERE b.booker_user_id=$1 ORDER BY s.start_at DESC LIMIT 200`, uid); err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true, "data": rows})
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
