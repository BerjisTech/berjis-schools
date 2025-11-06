import { Pipe, PipeTransform } from '@angular/core';
import { I18nService } from './i18n.service';

@Pipe({ name: 't', standalone: true })
export class TPipe implements PipeTransform {
  constructor(private i18n: I18nService) {}
  transform(value: string): string {
    return this.i18n.t(value);
  }
}

