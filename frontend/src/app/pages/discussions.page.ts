import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule, ActivatedRoute, Router } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-discussions',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './discussions.page.html'
})
export class DiscussionsPage implements OnInit {
  classId = '';
  threads = signal<any[]>([]);
  selected: any = null;
  newTitle = signal('');
  newPost = signal('');
  loading = signal(false);

  constructor(private svc: SchoolsService, private route: ActivatedRoute, private router: Router) {}

  async ngOnInit() {
    this.classId = this.route.snapshot.paramMap.get('id') || '';
    await this.reload();
  }

  async reload() {
    this.loading.set(true);
    try {
      this.threads.set(await this.svc.listDiscussions(this.classId));
      if (this.threads().length && !this.selected) {
        await this.open(this.threads()[0].id);
      }
    } finally { this.loading.set(false); }
  }

  async createThread() {
    if (!this.newTitle().trim()) return;
    const id = await this.svc.createDiscussion(this.classId, this.newTitle().trim());
    this.newTitle.set('');
    await this.reload();
    if (id) await this.open(id);
  }

  async open(id: string) {
    const data = await this.svc.getDiscussion(id);
    this.selected = data;
  }

  async post() {
    if (!this.selected?.discussion?.id || !this.newPost().trim()) return;
    await this.svc.postToDiscussion(this.selected.discussion.id, this.newPost().trim());
    this.newPost.set('');
    await this.open(this.selected.discussion.id);
  }

  async deletePost(postId: string) {
    if (!this.selected?.discussion?.id) return;
    const ok = await this.svc.deleteDiscussionPost(postId);
    if (ok) await this.open(this.selected.discussion.id);
  }
}
