import { UserRef, LessonContent } from './course';

export interface SchoolSummary {
  id: string;
  name: string;
  description?: string | null;
  isVerified?: boolean;
}

export interface SchoolInvite {
  id: string;
  email?: string;
  existingUserId?: string;
  role: 'admin' | 'tutor';
  status: string;
  invitedByUserId?: string;
  message?: string;
  createdAt: string;
  respondedAt?: string;
  token?: string;
}

export interface SchoolOverview {
  school: {
    id: string;
    ownerUserId: string;
    name: string;
    description?: string | null;
    isVerified: boolean;
    createdAt: string;
    updatedAt: string;
  };
  classes: SchoolClassOverview[];
}

export interface SchoolClassOverview {
  id: string;
  title: string;
  description?: string | null;
  tutor: UserRef;
  visibility: string;
  isPaid: boolean;
  priceCents: number;
  studentCount: number;
  students: UserRef[];
  subjects: SchoolSubjectOverview[];
}

export interface SchoolSubjectOverview {
  id: string;
  title: string;
  description?: string | null;
  orderIndex: number;
  lessons: SchoolLessonOverview[];
  tests: SchoolTestOverview[];
}

export interface SchoolLessonOverview {
  id: string;
  title: string;
  type: string;
  content?: LessonContent;
  orderIndex: number;
  isFree: boolean;
  tests: SchoolTestOverview[];
}

export interface SchoolTestOverview {
  id: string;
  title: string;
  description?: string | null;
  visibility: string;
  createdAt: string;
}
