package auth

import "github.com/jmoiron/sqlx"

func IsSchoolAdmin(db *sqlx.DB, schoolID string, userID string) (bool, error) {
    var exists bool
    err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role='admin' AND status='active')`, schoolID, userID)
    return exists, err
}

func IsSchoolTutor(db *sqlx.DB, schoolID string, userID string) (bool, error) {
    var exists bool
    err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM school_members WHERE school_id=$1 AND user_id=$2 AND role='tutor' AND status='active')`, schoolID, userID)
    return exists, err
}

func IsClassTutor(db *sqlx.DB, classID string, userID string) (bool, error) {
    var exists bool
    err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM classes WHERE id=$1 AND tutor_user_id=$2)`, classID, userID)
    return exists, err
}