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
  content?: LessonContent | any;
  orderIndex?: number;
  isFree?: boolean;
  estimatedMinutes?: number;
}

export interface Subject {
  id: string;
  classId: string;
  title: string;
  description?: string | null;
  orderIndex: number;
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
}

export type TestPlacementStrategy =
  | { kind: 'after_each_lesson' }
  | { kind: 'after_every_n_lessons'; n: number }
  | { kind: 'random'; count?: number }
  | { kind: 'manual' };

export type TestQuestionType = 'mcq'|'truefalse'|'short'|'long'|'essay'|'sentence'|'numeric'|'formula'|'code'|'match'|'ordering'|'fillblank'|'hotspot'|'dragdrop';

export interface TestQuestion {
  id: string;
  testId: string;
  qtype: TestQuestionType;
  prompt: string;
  options?: any; // shape depends on qtype; mcq e.g. { choices: [{key:'A', label:'...'}, ...] }
  answer?: any;  // canonical answer shape depends on qtype
  points: number;
  orderIndex: number;
}
