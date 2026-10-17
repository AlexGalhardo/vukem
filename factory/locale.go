package factory

import "strings"

// Locale selects the language and country of the data a Faker generates.
// The zero Locale keeps the original data set (mixed names, lorem ipsum),
// so seeded output from before locales existed does not change; the
// commerce, company and address methods use English data then.
type Locale struct{ d *localeData }

// LocaleEN returns the English (United States) locale.
func LocaleEN() Locale { return Locale{&enData} }

// LocalePTBR returns the Brazilian Portuguese locale.
func LocalePTBR() Locale { return Locale{&ptBRData} }

// Name is the locale's BCP 47 tag ("en", "pt-BR"), or "" for the zero
// Locale.
func (l Locale) Name() string {
	if l.d == nil {
		return ""
	}

	return l.d.tag
}

// localeData is one embedded data set. Product nouns carry their gender so
// pt-BR adjectives agree with them (adjectives hold "masculine|feminine").
type localeData struct {
	tag         string
	firstNames  []string
	lastNames   []string
	words       []string
	companyFmt  []string // %[1]s, %[2]s: last names; %[3]s: business noun
	companyNoun []string
	adjectives  []string
	materials   []string
	nouns       []noun
	categories  []string
	uses        []string
	benefits    []string
	productFmt  string // %[1]s: noun, %[2]s: adjective, %[3]s: material
	describeFmt string // %[1]s: product, %[2]s: use, %[3]s: benefit
	streetTypes []string
	streets     []string
	streetFmt   string // %[1]s: type, %[2]s: name, %[3]d: number
	cities      [][2]string
	addressFmt  string // %[1]s: street, %[2]s: city, %[3]s: state, %[4]s: postal code
}

type noun struct {
	word     string
	feminine bool
}

// adjective returns the form of adj ("masculine|feminine") that agrees
// with n.
func adjective(adj string, n noun) string {
	m, f, ok := strings.Cut(adj, "|")
	if ok && n.feminine {
		return f
	}

	return m
}

var (
	enData = localeData{
		tag: "en",
		firstNames: []string{
			"Olivia", "Liam", "Emma", "Noah", "Ava", "Oliver", "Sophia", "Elijah", "Isabella", "James",
			"Mia", "William", "Amelia", "Benjamin", "Harper", "Lucas", "Evelyn", "Henry", "Abigail", "Theodore",
			"Emily", "Jack", "Ella", "Levi", "Grace", "Daniel", "Chloe", "Samuel", "Nora", "Owen",
		},
		lastNames: []string{
			"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Wilson", "Anderson",
			"Taylor", "Thomas", "Moore", "Jackson", "Martin", "Lee", "Thompson", "White", "Harris", "Clark",
			"Lewis", "Walker", "Hall", "Allen", "Young", "King", "Wright", "Scott", "Green", "Baker",
		},
		words: strings.Fields(`time year people way day thing world life hand part place case week company
			system program question work number night point home water room mother area money story fact
			month lot right study book eye job word business issue side kind head house service friend
			power hour game line end member law car city community name team minute idea body information
			back parent face level office door health person art war history party result change morning
			reason research girl guy moment air teacher force education`),
		companyFmt: []string{"%[1]s %[3]s LLC", "%[1]s & %[2]s", "%[1]s %[3]s Inc.", "%[1]s %[3]s Co."},
		companyNoun: []string{
			"Supply",
			"Labs",
			"Goods",
			"Works",
			"Trading",
			"Logistics",
			"Foods",
			"Systems",
			"Studio",
			"Outfitters",
		},
		adjectives: []string{
			"Ergonomic", "Rustic", "Sleek", "Handcrafted", "Compact", "Durable", "Lightweight", "Classic",
			"Modern", "Premium", "Portable", "Vintage",
		},
		materials: []string{
			"Steel",
			"Bamboo",
			"Cotton",
			"Leather",
			"Oak",
			"Ceramic",
			"Glass",
			"Wool",
			"Granite",
			"Aluminum",
		},
		nouns: []noun{
			{word: "Chair"},
			{word: "Desk"},
			{word: "Lamp"},
			{word: "Backpack"},
			{word: "Mug"},
			{word: "Bottle"},
			{word: "Shelf"},
			{word: "Jacket"},
			{word: "Watch"},
			{word: "Notebook"},
			{word: "Speaker"},
			{word: "Table"},
		},
		categories: []string{
			"Electronics", "Home & Kitchen", "Books", "Sports & Outdoors", "Toys & Games", "Fashion", "Beauty",
			"Garden", "Automotive", "Pet Supplies", "Office Products", "Groceries",
		},
		uses: []string{
			"everyday use", "the office", "outdoor adventures", "small spaces", "gifting", "the whole family",
		},
		benefits: []string{
			"Ships in 24 hours.", "Backed by a one-year warranty.", "Easy to clean and assemble.", "Built to last.",
			"Designed for comfort.", "Available in several colors.",
		},
		productFmt:  "%[2]s %[3]s %[1]s",
		describeFmt: "%[1]s, perfect for %[2]s. %[3]s",
		streetTypes: []string{"St", "Ave", "Rd", "Blvd", "Ln", "Dr"},
		streets: []string{
			"Maple", "Oak", "Cedar", "Pine", "Elm", "Lake", "Hill", "Park", "Washington", "Lincoln", "Sunset", "River",
		},
		streetFmt: "%[3]d %[2]s %[1]s",
		cities: [][2]string{
			{"Springfield", "IL"},
			{"Riverside", "CA"},
			{"Fairview", "TX"},
			{"Franklin", "TN"},
			{"Greenville", "SC"},
			{"Madison", "WI"},
			{"Georgetown", "KY"},
			{"Salem", "OR"},
			{"Arlington", "VA"},
			{"Portland", "ME"},
		},
		addressFmt: "%[1]s, %[2]s, %[3]s %[4]s",
	}

	ptBRData = localeData{
		tag: "pt-BR",
		firstNames: []string{
			"Miguel", "Helena", "Arthur", "Alice", "Gael", "Laura", "Heitor", "Maria", "Theo", "Valentina",
			"Davi", "Heloísa", "Gabriel", "Cecília", "Bernardo", "Júlia", "Samuel", "Manuela", "João", "Sophia",
			"Pedro", "Lívia", "Rafael", "Beatriz", "Lucas", "Isadora", "Matheus", "Lorena", "Enzo", "Antônia",
		},
		lastNames: []string{
			"Silva", "Santos", "Oliveira", "Souza", "Rodrigues", "Ferreira", "Alves", "Pereira", "Lima", "Gomes",
			"Costa", "Ribeiro", "Martins", "Carvalho", "Almeida", "Lopes", "Soares", "Fernandes", "Vieira", "Barbosa",
			"Rocha", "Dias", "Nascimento", "Andrade", "Moreira", "Nunes", "Marques", "Machado", "Mendes", "Freitas",
		},
		words: strings.Fields(`tempo ano dia vida casa mundo trabalho parte lugar forma caso semana empresa
			sistema programa pergunta número noite ponto água sala mãe área dinheiro história fato mês livro
			olho palavra negócio lado tipo cabeça serviço amigo poder hora jogo linha fim membro lei carro
			cidade comunidade nome equipe minuto ideia corpo informação pai rosto nível escritório porta
			saúde pessoa arte guerra resultado mudança manhã razão pesquisa valor professor força educação`),
		companyFmt: []string{
			"%[1]s %[3]s Ltda", "%[1]s & %[2]s %[3]s Ltda", "%[3]s %[1]s S.A.", "%[1]s %[3]s ME",
		},
		companyNoun: []string{
			"Comércio", "Distribuidora", "Alimentos", "Tecnologia", "Móveis", "Confecções", "Logística", "Serviços",
			"Importadora", "Papelaria",
		},
		adjectives: []string{
			"Ergonômico|Ergonômica", "Rústico|Rústica", "Elegante|Elegante", "Artesanal|Artesanal",
			"Compacto|Compacta", "Resistente|Resistente", "Leve|Leve", "Clássico|Clássica", "Moderno|Moderna",
			"Premium|Premium", "Portátil|Portátil", "Vintage|Vintage",
		},
		materials: []string{
			"Aço",
			"Bambu",
			"Algodão",
			"Couro",
			"Carvalho",
			"Cerâmica",
			"Vidro",
			"Lã",
			"Granito",
			"Alumínio",
		},
		nouns: []noun{
			{word: "Cadeira", feminine: true},
			{word: "Mesa", feminine: true},
			{word: "Luminária", feminine: true},
			{word: "Mochila", feminine: true},
			{word: "Caneca", feminine: true},
			{word: "Garrafa", feminine: true},
			{word: "Estante", feminine: true},
			{word: "Jaqueta", feminine: true},
			{word: "Relógio"},
			{word: "Caderno"},
			{word: "Alto-falante"},
			{word: "Banco"},
		},
		categories: []string{
			"Eletrônicos", "Casa e Cozinha", "Livros", "Esporte e Lazer", "Brinquedos", "Moda", "Beleza",
			"Jardim", "Automotivo", "Pet Shop", "Papelaria e Escritório", "Mercado",
		},
		uses: []string{
			"o dia a dia", "o escritório", "aventuras ao ar livre", "espaços pequenos", "presentear", "toda a família",
		},
		benefits: []string{
			"Envio em 24 horas.", "Garantia de um ano.", "Fácil de limpar e montar.", "Feito para durar.",
			"Pensado para o seu conforto.", "Disponível em várias cores.",
		},
		productFmt:  "%[1]s %[2]s de %[3]s",
		describeFmt: "%[1]s, ideal para %[2]s. %[3]s",
		streetTypes: []string{"Rua", "Avenida", "Travessa", "Alameda", "Praça"},
		streets: []string{
			"das Flores", "dos Andradas", "Brasil", "Paulista", "Sete de Setembro", "XV de Novembro", "Santos Dumont",
			"Tiradentes", "das Palmeiras", "São João", "Getúlio Vargas", "Rio Branco",
		},
		streetFmt: "%[1]s %[2]s, %[3]d",
		cities: [][2]string{
			{"São Paulo", "SP"},
			{"Rio de Janeiro", "RJ"},
			{"Belo Horizonte", "MG"},
			{"Curitiba", "PR"},
			{"Porto Alegre", "RS"},
			{"Salvador", "BA"},
			{"Recife", "PE"},
			{"Fortaleza", "CE"},
			{"Florianópolis", "SC"},
			{"Goiânia", "GO"},
			{"Campinas", "SP"},
			{"Manaus", "AM"},
		},
		addressFmt: "%[1]s - %[2]s/%[3]s, %[4]s",
	}
)
