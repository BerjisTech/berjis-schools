import { Pipe, PipeTransform } from '@angular/core';
import { TerminologyService } from '../services/terminology.service';

@Pipe({ name: 'term', standalone: true })
export class TermPipe implements PipeTransform {
  constructor(private terms: TerminologyService) {}
  transform(key: string, fallback?: string): string { return this.terms.term(key, fallback); }
}

