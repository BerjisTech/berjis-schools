import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute } from '@angular/router';
import { SchoolsService } from '../../services/schools.service';
import { Lesson } from '../../interfaces/course';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';

interface QAAnswer { id: string; body: string; author?: string; points: number; createdAt: string }
interface QAQuestion { id: string; title: string; body: string; author?: string; points: number; createdAt: string; answers: QAAnswer[] }

@Component({
  selector: 'app-lesson-view',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './lesson-view.component.html'
})
export class LessonViewComponent implements OnInit {
  lesson = signal<Lesson | null>(null);
  loading = signal(true);
  qas = signal<QAQuestion[]>([]);
  newQuestionTitle = signal('');
  newQuestionBody = signal('');

  constructor(private route: ActivatedRoute, private svc: SchoolsService, private sanitizer: DomSanitizer) {}

  async ngOnInit() {
    const courseId = this.route.snapshot.paramMap.get('id')!;
    const lessonId = this.route.snapshot.paramMap.get('lessonId')!;
    // naive fetch: we only have subject->lessons list; in a real app we'd fetch by id
    // For now, search across subjects to find the lesson
    const subs = await this.svc.listSubjects(courseId);
    for (const s of subs) {
      const ls = await this.svc.listLessons(s.id);
      const hit = ls.find(l => `${l.id}` === lessonId);
      if (hit) { this.lesson.set(hit); break; }
    }
    this.loading.set(false);
    this.loadLocalQA(lessonId);
  }

  contentKind = computed(() => this.lesson()?.type ?? 'text');

  private loadLocalQA(lessonId: string) {
    try {
      const raw = localStorage.getItem(`lesson:${lessonId}:qa`);
      if (raw) this.qas.set(JSON.parse(raw));
    } catch {}
  }
  private saveLocalQA(lessonId: string) {
    try { localStorage.setItem(`lesson:${lessonId}:qa`, JSON.stringify(this.qas())); } catch {}
  }

  askQuestion() {
    const l = this.lesson(); if (!l) return;
    const title = this.newQuestionTitle().trim();
    const body = this.newQuestionBody().trim();
    if (!title || !body) return;
    const q: QAQuestion = { id: crypto.randomUUID(), title, body, author: 'You', points: 0, createdAt: new Date().toISOString(), answers: [] };
    this.qas.update(arr => [q, ...arr]);
    this.newQuestionTitle.set('');
    this.newQuestionBody.set('');
    this.saveLocalQA(`${l.id}`);
  }

  upvoteQuestion(q: QAQuestion) {
    const l = this.lesson(); if (!l) return;
    q.points += 1; this.qas.update(a => [...a]); this.saveLocalQA(`${l.id}`);
  }
  downvoteQuestion(q: QAQuestion) {
    const l = this.lesson(); if (!l) return;
    q.points -= 1; this.qas.update(a => [...a]); this.saveLocalQA(`${l.id}`);
  }
  addAnswer(q: QAQuestion, body: string) {
    const l = this.lesson(); if (!l) return;
    const a: QAAnswer = { id: crypto.randomUUID(), body, author: 'You', points: 0, createdAt: new Date().toISOString() };
    q.answers.unshift(a);
    this.qas.update(arr => [...arr]);
    this.saveLocalQA(`${l.id}`);
  }
  voteAnswer(q: QAQuestion, a: QAAnswer, delta: number) {
    const l = this.lesson(); if (!l) return;
    a.points += delta; this.qas.update(arr => [...arr]); this.saveLocalQA(`${l.id}`);
  }

  simulationUrl(lesson: Lesson): SafeResourceUrl | null {
    const url = (lesson?.content as any)?.sandboxUrl;
    if (!url || typeof url !== 'string' || !url.trim()) return null;
    return this.sanitizer.bypassSecurityTrustResourceUrl(url);
  }
}
