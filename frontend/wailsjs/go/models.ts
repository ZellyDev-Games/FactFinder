export namespace repo {
	
	export class Provider {
	    FilePath: string;
	    Name: string;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.FilePath = source["FilePath"];
	        this.Name = source["Name"];
	    }
	}

}

