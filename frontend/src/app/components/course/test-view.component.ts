import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute } from '@angular/router';
import { SchoolsService } from '../../services/schools.service';
import { TestItem, TestQuestion } from '../../interfaces/course';
import { QUESTION_TYPE_OPTIONS, QuestionType, toBackendQType } from '../../constants/question-types';
import { urlFor } from '../../util';

@Component({
  selector: 'app-test-view',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './test-view.component.html'
})
export class TestViewComponent implements OnInit {
  testId = signal<string>('');
  title = signal<string>('');
  loading = signal(true);
  test = signal<TestItem | null>(null);
  questions = signal<TestQuestion[]>([]);
  answers = signal<Record<string, any>>({});
  creating = signal(false);
  createdMsg = signal('');
  canEdit = signal(false);
  // authoring state
  qType = signal<QuestionType>('multiple_choice');
  qPrompt = signal('');
  qAllowMultiple = signal(false);
  qChoicesText = signal('A. Option 1\nB. Option 2');
  qOptionsJSON = signal('');
  qAnswerJSON = signal('');
  get typeOptions() { return QUESTION_TYPE_OPTIONS }
  get objectiveOptions() { return QUESTION_TYPE_OPTIONS.filter(o => o.group === 'Objective') }
  get subjectiveOptions() { return QUESTION_TYPE_OPTIONS.filter(o => o.group === 'Subjective') }
  get interactiveOptions() { return QUESTION_TYPE_OPTIONS.filter(o => o.group === 'Interactive') }

  constructor(private route: ActivatedRoute, private svc: SchoolsService) {}

  async ngOnInit() {
    const testId = this.route.snapshot.paramMap.get('testId')!;
    this.testId.set(testId);
    try {
      const data = await this.svc.getTestById(testId);
      if (data) {
        this.test.set(data.test);
        this.title.set(data.test.title);
        this.questions.set(data.questions);
        this.canEdit.set(!!data.canEdit);
      }
    } finally {
      this.loading.set(false);
    }
  }

  // Answer helpers for template-safe updates
  getAnswer(qid: string): any { return this.answers()[qid] }
  setAnswer(qid: string, v: any) { this.answers.update(a => ({ ...a, [qid]: v })) }

  getBlankAnswer(qid: string, bid: string): any { const obj = this.answers()[qid] || {}; return obj[bid] ?? '' }
  setBlankAnswer(qid: string, bid: string, v: any) { this.answers.update(a => ({ ...a, [qid]: { ...(a[qid]||{}), [bid]: v } })) }

  getMatchSelection(qid: string, leftId: string): any { const obj = this.answers()[qid] || {}; return obj[leftId] ?? '' }
  setMatchAnswer(qid: string, leftId: string, rightId: string) { this.answers.update(a => ({ ...a, [qid]: { ...(a[qid]||{}), [leftId]: rightId } })) }

  getOrdering(q: TestQuestion): string[] {
    const cur = this.answers()[q.id];
    if (Array.isArray(cur) && cur.length) return cur;
    const items = (q as any).options?.items || [];
    return items.map((i: any) => i.id);
  }
  labelForOrdering(q: TestQuestion, itemId: string): string {
    const items = (q as any).options?.items || [];
    const it = items.find((x: any) => x.id === itemId);
    return it?.label || itemId;
  }
  moveOrderingUp(q: TestQuestion, idx: number) {
    const arr = [...this.getOrdering(q)];
    if (idx > 0) { const t = arr[idx-1]; arr[idx-1] = arr[idx]; arr[idx] = t; this.setAnswer(q.id, arr) }
  }
  moveOrderingDown(q: TestQuestion, idx: number) {
    const arr = [...this.getOrdering(q)];
    if (idx < arr.length - 1) { const t = arr[idx+1]; arr[idx+1] = arr[idx]; arr[idx] = t; this.setAnswer(q.id, arr) }
  }

  // Drag & Drop helpers
  private getAllItemIds(q: TestQuestion): string[] { const items = (q as any).options?.items || []; return items.map((i: any) => i.id) }
  private getPlacements(q: TestQuestion): Record<string, string[]> { const ans = this.getAnswer(q.id) || {}; return ans.placements || {} }
  getUnassignedItems(q: TestQuestion): string[] {
    const all = this.getAllItemIds(q);
    const placements = this.getPlacements(q);
    const assigned = new Set<string>();
    Object.values(placements).forEach(arr => (arr||[]).forEach(id => assigned.add(id)));
    return all.filter(id => !assigned.has(id));
  }
  getBinItems(q: TestQuestion, binId: string): string[] { const placements = this.getPlacements(q); return (placements[binId] || []).slice() }
  onDragStart(ev: DragEvent, itemId: string) { try { ev.dataTransfer?.setData('text/plain', itemId) } catch {} }
  dropToUnassigned(q: TestQuestion, ev: DragEvent) {
    ev.preventDefault();
    const item = ev.dataTransfer?.getData('text/plain'); if (!item) return;
    const ans = this.getAnswer(q.id) || {}; const placements = { ...(ans.placements || {}) } as Record<string,string[]>;
    for (const b in placements) { placements[b] = (placements[b] || []).filter(x => x !== item) }
    placements['_unassigned'] = [...(placements['_unassigned'] || []), item];
    this.setAnswer(q.id, { placements });
  }
  dropToBin(q: TestQuestion, binId: string, ev: DragEvent) {
    ev.preventDefault();
    const item = ev.dataTransfer?.getData('text/plain'); if (!item) return;
    const ans = this.getAnswer(q.id) || {}; const placements = { ...(ans.placements || {}) } as Record<string,string[]>;
    for (const b in placements) { placements[b] = (placements[b] || []).filter(x => x !== item) }
    placements[binId] = [...(placements[binId] || []), item];
    this.setAnswer(q.id, { placements });
  }

  // MCQ multi-select toggle
  toggleMultiSelect(qid: string, key: string, checked: boolean) {
    const val = this.getAnswer(qid);
    let arr: string[] = Array.isArray(val) ? [...val] : [];
    const idx = arr.indexOf(key);
    if (checked && idx < 0) arr.push(key);
    if (!checked && idx >= 0) arr.splice(idx, 1);
    this.setAnswer(qid, arr);
  }

  // Hotspot toggle
  toggleHotspot(qid: string, regionId: string) {
    const val = this.getAnswer(qid);
    let arr: string[] = Array.isArray(val) ? [...val] : [];
    const idx = arr.indexOf(regionId);
    if (idx >= 0) arr.splice(idx, 1); else arr.push(regionId);
    this.setAnswer(qid, arr);
  }

  private buildOptionsAndAnswer(): { options: any; answer: any } {
    // If manual JSON provided, prefer it
    try {
      const o = this.qOptionsJSON().trim();
      const a = this.qAnswerJSON().trim();
      if (o || a) {
        return { options: o ? JSON.parse(o) : null, answer: a ? JSON.parse(a) : null };
      }
    } catch {}
    const t = this.qType();
    if (t === 'multiple_choice' || t === 'multiple_select') {
      const lines = this.qChoicesText().split(/\r?\n/).map(s => s.trim()).filter(Boolean);
      const choices = lines.map(line => {
        const m = line.match(/^([A-Za-z])\.?\s*(.*)$/);
        if (m) return { key: m[1].toUpperCase(), label: m[2] };
        return { key: String.fromCharCode(65 + choices.length), label: line } as any;
      });
      const options = { choices, allowMultiple: t === 'multiple_select' };
      const answer = t === 'multiple_select' ? { keys: [] } : { key: choices[0]?.key };
      return { options, answer };
    }
    if (t === 'true_false') {
      return { options: {}, answer: { value: true } };
    }
    if (t === 'fill_blank') {
      return { options: { blanks: [{ id: 'b1', kind: 'text', synonyms: [] }] }, answer: { b1: '' } };
    }
    if (t === 'numeric') {
      return { options: { tolerance: 0 }, answer: { value: 0 } };
    }
    if (t === 'matching') {
      return { options: { left: [{ id: 'l1', label: 'Left 1' }], right: [{ id: 'r1', label: 'Right 1' }] }, answer: { pairs: [['l1','r1']] } };
    }
    if (t === 'ordering') {
      return { options: { items: [{ id: 'i1', label: 'Item 1' }, { id: 'i2', label: 'Item 2' }] }, answer: { order: ['i1','i2'] } };
    }
    // subjective defaults
    return { options: {}, answer: null };
  }

  async createQuestion() {
    const testId = this.testId(); if (!testId) return;
    this.creating.set(true); this.createdMsg.set('');
    try {
      const { options, answer } = this.buildOptionsAndAnswer();
      const body = {
        qtype: toBackendQType(this.qType()),
        prompt: this.qPrompt(),
        options,
        answer,
      };
      const res = await fetch(`${urlFor('schools-api')}/v1/tests/${testId}/questions`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
      });
      const j = await res.json();
      if (j?.success) {
        const q = j.data as TestQuestion;
        this.questions.update(arr => [...arr, q]);
        this.createdMsg.set('Question created');
        this.qPrompt.set('');
      } else {
        this.createdMsg.set(j?.message || 'Failed');
      }
    } finally {
      this.creating.set(false);
    }
  }

  async start() { await this.svc.startTestAttempt(this.testId()) }
  async save() { await this.svc.saveTestAttempt(this.testId(), this.answers()) }
  async submit() {
    const res = await this.svc.submitTestAttempt(this.testId(), this.answers());
    if (res) this.createdMsg.set(`Submitted: ${Math.round((res.score || 0)*100)/100}% (${res.earned}/${res.max})`);
  }
}
