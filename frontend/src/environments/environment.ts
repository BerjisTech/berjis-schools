const w = typeof window !== 'undefined' ? (window as any) : {};

export const environment = {
  production: false,
  apiBase: w && typeof w.__BERJIS_API__ === 'string' && w.__BERJIS_API__.trim().length
    ? w.__BERJIS_API__.trim()
    : 'https://api.berjis.tech',
  schoolsApiBase: w && typeof w.__SCHOOLS_API__ === 'string' && w.__SCHOOLS_API__.trim().length
    ? w.__SCHOOLS_API__.trim()
    : 'https://schools-api.berjis.tech'
};
