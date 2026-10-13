// The CK family is not Java's private property: every language that declares
// classes gets the same five measurements. This fixture is the non-Java half
// of TestCatalogCoversEveryMeasuredMetric — if TypeScript stopped emitting
// class metrics, the class ids would silently drop out of the catalogue and
// out of every audit built on it.
export interface Repo {
  find(id: string): string;
}

export class Service implements Repo {
  private repo: Store;
  count: number = 0;

  constructor(repo: Store) {
    this.repo = repo;
  }

  find(id: string): string {
    if (id === "") {
      return "";
    }
    return this.repo.read(id);
  }
}

export class Store extends Base {
  private items: Map<string, string> = new Map();

  read(id: string): string {
    return this.items.get(id) ?? "";
  }
}
