export type QuestionType =
  | 'multiple_choice'
  | 'multiple_select'
  | 'true_false'
  | 'fill_blank'
  | 'essay'
  | 'short_answer'
  | 'code'
  | 'file_upload'
  | 'audio'
  | 'video'
  | 'drag_drop'
  | 'matching'
  | 'ordering'
  | 'numeric';

export const QUESTION_TYPE_OPTIONS: { value: QuestionType; label: string; group: 'Objective'|'Subjective'|'Interactive'|'Composite' }[] = [
  { value: 'multiple_choice', label: 'Multiple Choice', group: 'Objective' },
  { value: 'multiple_select', label: 'Multiple Select', group: 'Objective' },
  { value: 'true_false', label: 'True / False', group: 'Objective' },
  { value: 'fill_blank', label: 'Fill in the Blank', group: 'Objective' },
  { value: 'numeric', label: 'Numeric / Calculation', group: 'Objective' },
  { value: 'matching', label: 'Matching', group: 'Objective' },
  { value: 'ordering', label: 'Ordering / Sequencing', group: 'Objective' },

  { value: 'short_answer', label: 'Short Answer', group: 'Subjective' },
  { value: 'essay', label: 'Essay / Long Answer', group: 'Subjective' },
  { value: 'code', label: 'Code Response', group: 'Subjective' },
  { value: 'file_upload', label: 'File Upload', group: 'Subjective' },
  { value: 'audio', label: 'Audio Response', group: 'Subjective' },
  { value: 'video', label: 'Video Response', group: 'Subjective' },

  { value: 'drag_drop', label: 'Drag & Drop', group: 'Interactive' },
];

// Canonical mapping between frontend QuestionType and backend qtype (database)
export function toBackendQType(t: QuestionType): string {
  switch (t) {
    case 'multiple_choice': return 'mcq';
    case 'multiple_select': return 'mcq'; // with options.allowMultiple=true
    case 'true_false': return 'truefalse';
    case 'fill_blank': return 'fillblank';
    case 'essay': return 'essay';
    case 'short_answer': return 'short';
    case 'code': return 'code';
    case 'numeric': return 'numeric';
    case 'matching': return 'match';
    case 'ordering': return 'ordering';
    case 'drag_drop': return 'dragdrop';
    // These are not yet stored as distinct backend qtypes; placeholder mapping to essay/short.
    case 'file_upload': return 'essay';
    case 'audio': return 'essay';
    case 'video': return 'essay';
  }
}

export function fromBackendQType(q: string): QuestionType | undefined {
  switch ((q || '').toLowerCase()) {
    case 'mcq': return 'multiple_choice';
    case 'truefalse': return 'true_false';
    case 'fillblank': return 'fill_blank';
    case 'essay': return 'essay';
    case 'long': return 'essay';
    case 'short': return 'short_answer';
    case 'sentence': return 'short_answer';
    case 'code': return 'code';
    case 'numeric': return 'numeric';
    case 'match': return 'matching';
    case 'ordering': return 'ordering';
    case 'dragdrop': return 'drag_drop';
    default: return undefined;
  }
}

