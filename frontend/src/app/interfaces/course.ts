// Using string for UUID compatibility across environments

export interface UserRef {
  id?: string;
  name?: string;
  avatar?: string;
}

export interface LessonContentText {
  type: 'text';
  html?: string;
  markdown?: string;
}

export interface LessonContentAudio {
  type: 'audio';
  url: string;
  durationSec?: number;
}

export interface LessonContentVideo {
  type: 'video';
  url: string;
  durationSec?: number;
}

export interface LessonContentLive {
  type: 'live';
  mode: 'one_on_one' | 'group';
  startsAt?: string; // ISO timestamp
  endsAt?: string;   // ISO timestamp
}

export interface LessonContentSimulation {
  type: 'simulation';
  sandboxUrl?: string;
  instructions?: string;
}

export type LessonContent = LessonContentText | LessonContentAudio | LessonContentVideo | LessonContentLive | LessonContentSimulation;

export interface Lesson {
  id: string;
  name?: string;
  title?: string;
  description?: string;
  subjectId?: string;
  type?: 'text' | 'audio' | 'video' | 'live' | 'simulation';
  content?: LessonContent;
  orderIndex?: number;
  isFree?: boolean;
  estimatedMinutes?: number;
  status?: 'active' | 'archived' | 'deleted';
}

export interface Subject {
  id: string;
  classId: string;
  title: string;
  description?: string | null;
  orderIndex: number;
  status?: 'active' | 'archived' | 'deleted';
}

export interface Course {
  id: string;
  name: string;
  title?: string;
  description: string;
  lessons?: Lesson[];
  level?: string;
  organizer?: UserRef;
  progress?: number;
  poster?: string;
  rating?: number;
  isPaid?: boolean;
  priceCents?: number;
  visibility?: 'school' | 'private' | 'public';
  tutorUserId?: string;
  studentCount?: number;
  subjectCount?: number;
  lessonCount?: number;
  testCount?: number;
  schoolId?: string | null;
  isEnrolled?: boolean;
  status?: 'active' | 'archived' | 'deleted';
  subjects?: Subject[];
}

export interface TestItem {
  id: string;
  classId?: string;
  schoolId?: string | null;
  subjectId?: string | null;
  lessonId?: string | null;
  title: string;
  description?: string | null;
  visibility: 'public' | 'private';
  createdByUserId?: string;
  createdAt?: string;
  status?: 'active' | 'archived' | 'deleted';
}

export type TestPlacementStrategy =
  | { kind: 'after_each_lesson' }
  | { kind: 'after_every_n_lessons'; n: number }
  | { kind: 'random'; count?: number }
  | { kind: 'manual' };

export type TestQuestionType = 'mcq'|'truefalse'|'short'|'long'|'essay'|'sentence'|'numeric'|'formula'|'code'|'match'|'ordering'|'fillblank'|'hotspot'|'dragdrop';

// Common option/answer shapes by qtype (minimal but typed)
export interface Choice { key: string; label: string }
export interface LabeledItem { id: string; label: string }

export type TestOptions =
  | { choices: Choice[]; allowMultiple?: boolean }
  | { tolerance?: number }
  | { blanks: Array<{ id: string; kind: 'text' | 'number'; synonyms?: string[] }> }
  | { left: LabeledItem[]; right: LabeledItem[] }
  | { items: LabeledItem[] }
  | { imageUrl?: string; width?: number; height?: number; regions?: Array<{ id: string; coords?: number[] }> }
  | { bins?: LabeledItem[]; items: LabeledItem[] }
  | Record<string, unknown>;

export type TestAnswer = unknown;

export interface TestQuestion {
  id: string;
  testId: string;
  qtype: TestQuestionType;
  prompt: string;
  options?: TestOptions; // typed minimal union per qtype
  answer?: TestAnswer;  // typed minimal union per qtype
  points: number;
  orderIndex: number;
}
