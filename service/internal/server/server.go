package server

import (
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"

    "github.com/berjistech/berjis-ecosystem/schools/service/internal/auth"
    "github.com/gofiber/fiber/v2"
    "github.com/gofiber/fiber/v2/middleware/cors"
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

    // Auth verify against Core API
    getUserID := func(c *fiber.Ctx) (string, error) {
        req, _ := http.NewRequest("GET", strings.TrimRight(opts.CoreAPIBase, "/")+"/v1/auth/verify", nil)
        if v := c.Get("Authorization"); v != "" { req.Header.Set("Authorization", v) }
        if v := c.Get("Cookie"); v != "" { req.Header.Set("Cookie", v) }
        client := &http.Client{ Timeout: 3 * time.Second }
        resp, err := client.Do(req)
        if err != nil { return "", err }
        defer resp.Body.Close()
        var raw map[string]any
        if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil { return "", err }
        data, _ := raw["data"].(map[string]any)
        if data == nil { return "", fiber.ErrUnauthorized }
        if valid, _ := data["valid"].(bool); !valid { return "", fiber.ErrUnauthorized }
        if uidAny, ok := data["uid"]; ok {
            switch v := uidAny.(type) {
            case float64:
                return fmt.Sprintf("%0.0f", v), nil
            case string:
                return v, nil
            }
        }
        if uidStr, ok := data["userId"].(string); ok && uidStr != "" { return uidStr, nil }
        return "", fiber.ErrUnauthorized
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
        if opts.DB == nil { return fiber.ErrInternalServerError }
        rows := []school{}
        if err := opts.DB.Select(&rows, `SELECT id, owner_user_id, name, description, is_verified, created_at, updated_at FROM schools ORDER BY created_at DESC`); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    type schoolIn struct { Name string `json:"name"`; Description *string `json:"description"` }
    app.Post("/v1/schools", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var in schoolIn
        if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Name)=="" { return fiber.ErrBadRequest }
        var out school
        err = opts.DB.Get(&out, `INSERT INTO schools (owner_user_id, name, description) VALUES ($1,$2,$3)
            RETURNING id, owner_user_id, name, description, is_verified, created_at, updated_at`, uid, in.Name, in.Description)
        if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
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
        if opts.DB == nil { return fiber.ErrInternalServerError }
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
        SchoolID   *string `json:"schoolId"`
        Title      string  `json:"title"`
        Description *string `json:"description"`
        Visibility string  `json:"visibility"`
        IsPaid     *bool   `json:"isPaid"`
        PriceCents *int    `json:"priceCents"`
    }
    app.Post("/v1/classes", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var in classIn
        if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title)=="" { return fiber.ErrBadRequest }
        vis := in.Visibility; if vis=="" { vis = "school" }
        isPaid := false; if in.IsPaid != nil { isPaid = *in.IsPaid }
        price := 0; if in.PriceCents != nil { price = *in.PriceCents }
        if in.SchoolID != nil && *in.SchoolID != "" {
            okA, _ := auth.IsSchoolAdmin(opts.DB, *in.SchoolID, uid)
            okT, _ := auth.IsSchoolTutor(opts.DB, *in.SchoolID, uid)
            if !okA && !okT { return fiber.ErrForbidden }
        }
        var out class
        err = opts.DB.Get(&out, `INSERT INTO classes (school_id, tutor_user_id, title, description, visibility, is_paid, price_cents)
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, tutor_user_id, title, description, visibility, is_paid, price_cents, created_at, updated_at`,
            in.SchoolID, uid, in.Title, in.Description, vis, isPaid, price)
        if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true, "data": out})
    })

    app.Post("/v1/classes/:id/enroll", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        id := c.Params("id")
        if _, err := opts.DB.Exec(`INSERT INTO class_enrollments (class_id, student_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, uid); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true})
    })

    // --- Subjects ---
    type subject struct {
        ID         string  `json:"id" db:"id"`
        ClassID    string  `json:"classId" db:"class_id"`
        Title      string  `json:"title" db:"title"`
        Description *string `json:"description,omitempty" db:"description"`
        OrderIndex int     `json:"orderIndex" db:"order_index"`
    }
    app.Get("/v1/subjects", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        classID := c.Query("class_id")
        rows := []subject{}
        if classID=="" { return fiber.ErrBadRequest }
        if err := opts.DB.Select(&rows, `SELECT id, class_id, title, description, order_index FROM subjects WHERE class_id=$1 ORDER BY order_index ASC, title ASC`, classID); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    type subjectIn struct { ClassID string `json:"classId"`; Title string `json:"title"`; Description *string `json:"description"`; OrderIndex *int `json:"orderIndex"` }
    app.Post("/v1/subjects", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        _ = uid
        var in subjectIn; if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title)=="" || strings.TrimSpace(in.ClassID)=="" { return fiber.ErrBadRequest }
        okCT, _ := auth.IsClassTutor(opts.DB, in.ClassID, uid)
        if !okCT {
            var sid *string
            _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, in.ClassID)
            if sid != nil && *sid != "" { okA, _ := auth.IsSchoolAdmin(opts.DB, *sid, uid); if !okA { return fiber.ErrForbidden } } else { return fiber.ErrForbidden }
        }
        ord := 0; if in.OrderIndex != nil { ord = *in.OrderIndex }
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
        if opts.DB == nil { return fiber.ErrInternalServerError }
        sid := c.Query("subject_id"); if sid=="" { return fiber.ErrBadRequest }
        rows := []lesson{}
        if err := opts.DB.Select(&rows, `SELECT id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free FROM lessons WHERE subject_id=$1 ORDER BY order_index ASC, title ASC`, sid); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    type lessonIn struct { SubjectID string `json:"subjectId"`; Title string `json:"title"`; Type string `json:"type"`; Content json.RawMessage `json:"content"`; OrderIndex *int `json:"orderIndex"`; IsFree *bool `json:"isFree"` }
    app.Post("/v1/lessons", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var in lessonIn; if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.SubjectID)=="" || strings.TrimSpace(in.Title)=="" || strings.TrimSpace(in.Type)=="" { return fiber.ErrBadRequest }
        var classID string
        if err := opts.DB.Get(&classID, `SELECT class_id FROM subjects WHERE id=$1`, in.SubjectID); err != nil { return fiber.ErrBadRequest }
        okCT, _ := auth.IsClassTutor(opts.DB, classID, uid)
        var okA bool
        if !okCT { var sid *string; _ = opts.DB.Get(&sid, `SELECT school_id FROM classes WHERE id=$1`, classID); if sid != nil && *sid != "" { okA, _ = auth.IsSchoolAdmin(opts.DB, *sid, uid) } }
        if !okCT && !okA { return fiber.ErrForbidden }
        ord := 0; if in.OrderIndex != nil { ord = *in.OrderIndex }
        free := false; if in.IsFree != nil { free = *in.IsFree }
        var out lesson
        if err := opts.DB.Get(&out, `INSERT INTO lessons (subject_id, title, type, content, order_index, is_free) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, subject_id, title, type, COALESCE(content,'null'::jsonb) AS content, order_index, is_free`, in.SubjectID, in.Title, in.Type, nullIfEmptyJSON(in.Content), ord, free); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true, "data": out})
    })

    // --- Tests ---
    type test struct {
        ID        string    `json:"id" db:"id"`
        SchoolID  *string   `json:"schoolId,omitempty" db:"school_id"`
        SubjectID *string   `json:"subjectId,omitempty" db:"subject_id"`
        LessonID  *string   `json:"lessonId,omitempty" db:"lesson_id"`
        Title     string    `json:"title" db:"title"`
        Description *string `json:"description,omitempty" db:"description"`
        Visibility string   `json:"visibility" db:"visibility"`
        CreatedBy string    `json:"createdByUserId" db:"created_by_user_id"`
        CreatedAt time.Time `json:"createdAt" db:"created_at"`
    }
    app.Get("/v1/tests", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        sid := c.Query("subject_id"); lid := c.Query("lesson_id")
        rows := []test{}
        var err error
        if lid != "" {
            err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests WHERE lesson_id=$1 ORDER BY created_at DESC`, lid)
        } else if sid != "" {
            err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests WHERE subject_id=$1 ORDER BY created_at DESC`, sid)
        } else {
            err = opts.DB.Select(&rows, `SELECT id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at FROM tests ORDER BY created_at DESC`)
        }
        if err != nil { return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()}) }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    type testIn struct { SchoolID *string `json:"schoolId"`; SubjectID *string `json:"subjectId"`; LessonID *string `json:"lessonId"`; Title string `json:"title"`; Description *string `json:"description"`; Visibility string `json:"visibility"` }
    app.Post("/v1/tests", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var in testIn; if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Title)=="" { return fiber.ErrBadRequest }
        vis := in.Visibility; if vis=="" { vis = "private" }
        var out test
        if err := opts.DB.Get(&out, `INSERT INTO tests (school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id)
            VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, school_id, subject_id, lesson_id, title, description, visibility, created_by_user_id, created_at`, in.SchoolID, in.SubjectID, in.LessonID, in.Title, in.Description, vis, uid); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true, "data": out})
    })

    // --- Guardians ---
    app.Get("/v1/guardians/children", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var rows []struct{ Child string `json:"childUserId" db:"child_user_id"` }
        if err := opts.DB.Select(&rows, `SELECT child_user_id FROM guardians_children WHERE guardian_user_id=$1`, uid); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true, "data": rows})
    })
    app.Post("/v1/guardians/link", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var body struct{ ChildUserID string `json:"childUserId"` }
        if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.ChildUserID)=="" { return fiber.ErrBadRequest }
        if _, err := opts.DB.Exec(`INSERT INTO guardians_children (guardian_user_id, child_user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, body.ChildUserID); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true})
    })

    // --- Progress ---
    app.Get("/v1/progress/overview", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        userID := c.Query("user_id", uid)
        if userID != uid {
            var ok bool
            if err := opts.DB.Get(&ok, `SELECT EXISTS (SELECT 1 FROM guardians_children WHERE guardian_user_id=$1 AND child_user_id=$2)`, uid, userID); err != nil { return fiber.ErrForbidden }
            if !ok { return fiber.ErrForbidden }
        }
        var classes int
        _ = opts.DB.Get(&classes, `SELECT COUNT(*) FROM class_enrollments WHERE student_user_id=$1`, userID)
        var completed int
        _ = opts.DB.Get(&completed, `SELECT COUNT(*) FROM lesson_progress WHERE student_user_id=$1 AND status='completed'`, userID)
        var attempted int
        _ = opts.DB.Get(&attempted, `SELECT COUNT(*) FROM test_attempts WHERE student_user_id=$1 AND status<>'in_progress'`, userID)
        return c.JSON(fiber.Map{"success": true, "data": fiber.Map{"classesEnrolled": classes, "lessonsCompleted": completed, "testsAttempted": attempted}})
    })
    app.Post("/v1/progress/lessons", func(c *fiber.Ctx) error {
        if opts.DB == nil { return fiber.ErrInternalServerError }
        uid, err := getUserID(c); if err != nil { return fiber.ErrUnauthorized }
        var body struct{ LessonID string `json:"lessonId"`; Status string `json:"status"` }
        if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.LessonID)=="" { return fiber.ErrBadRequest }
        st := body.Status; if st=="" { st = "in_progress" }
        if st != "in_progress" && st != "completed" { return fiber.ErrBadRequest }
        if _, err := opts.DB.Exec(`INSERT INTO lesson_progress (lesson_id, student_user_id, status, last_viewed_at, updated_at)
          VALUES ($1,$2,$3,now(),now())
          ON CONFLICT (lesson_id, student_user_id) DO UPDATE SET status=EXCLUDED.status, last_viewed_at=now(), updated_at=now()`, body.LessonID, uid, st); err != nil {
            return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
        }
        return c.JSON(fiber.Map{"success": true})
    })

    return app
}

func nullIfEmptyJSON(j json.RawMessage) any { if len(j)==0 || string(j)=="null" { return nil }; return j }