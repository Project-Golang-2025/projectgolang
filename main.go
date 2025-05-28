package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

const vacanciesFile = "vacancies.json"
const joobleAPIKey = "ded3c1eb-8286-44c5-b34f-103bc0ffbc4d"
const settingsFile = "settings.json" // ДОБАВЛЕНО

// ДОБАВЛЕНО: Структура для хранения цветов темы
type Theme struct {
	Name        string
	Background  walk.Color
	Text        walk.Color
	ButtonBG    walk.Color
	ButtonText  walk.Color
	TableBG     walk.Color
	TableText   walk.Color
	PanelBG     walk.Color
	BorderColor walk.Color
}

// ДОБАВЛЕНО: Глобальные темы
var (
	lightTheme = Theme{
		Name:        "Светлая",
		Background:  walk.RGB(255, 255, 255),
		Text:        walk.RGB(0, 0, 0),
		ButtonBG:    walk.RGB(235, 235, 235),
		ButtonText:  walk.RGB(0, 0, 0),
		TableBG:     walk.RGB(255, 255, 255),
		TableText:   walk.RGB(0, 0, 0),
		PanelBG:     walk.RGB(245, 245, 245),
		BorderColor: walk.RGB(200, 200, 200),
	}

	darkTheme = Theme{
		Name:        "Тёмная",
		Background:  walk.RGB(30, 30, 30),    // Тёмно-серый фон
		Text:        walk.RGB(220, 220, 220), // Светло-серый текст
		ButtonBG:    walk.RGB(45, 45, 45),    // Чуть светлее фона для кнопок
		ButtonText:  walk.RGB(220, 220, 220), // Светло-серый текст кнопок
		TableBG:     walk.RGB(35, 35, 35),    // Немного светлее фона для таблицы
		TableText:   walk.RGB(220, 220, 220), // Светло-серый текст таблицы
		PanelBG:     walk.RGB(40, 40, 40),    // Промежуточный серый для панелей
		BorderColor: walk.RGB(60, 60, 60),    // Более светлый серый для границ
	}
)

// ДОБАВЛЕНО: Текущая тема
var currentTheme = lightTheme

// Vacancy определяет структуру для хранения данных о вакансии
type Vacancy struct {
	Title           string   `json:"title"`
	Company         string   `json:"company"`
	Description     string   `json:"description"`
	Keywords        []string `json:"keywords"`
	SourceURL       string   `json:"sourceURL,omitempty"`
	Status          string   `json:"status,omitempty"`
	ExperienceLevel string   `json:"experienceLevel,omitempty"` // ДОБАВЛЕНО: Уровень опыта
	Notes           string   `json:"notes,omitempty"`           // ДОБАВЛЕНО: Заметки
	ResumePath      string   `json:"resumePath,omitempty"`      // ДОБАВЛЕНО: Путь к файлу резюме
	ResumeFileName  string   `json:"resumeFileName,omitempty"`  // ДОБАВЛЕНО: Имя файла резюме
}

// Глобальный срез для хранения вакансий
var allVacancies = []Vacancy{} // Теперь инициализируем пустым, будем загружать из файла
var allVacanciesMutex = &sync.Mutex{}

// Карта цветов для статусов
var statusColors = map[string]walk.Color{
	"Новая": walk.RGB(220, 255, 220), // светло-зеленый
	"Планирую откликнуться": walk.RGB(255, 255, 200), // светло-желтый
	"Откликнулся":           walk.RGB(210, 240, 255), // светло-голубой
	"Тестовое задание":      walk.RGB(255, 230, 200), // светло-оранжевый
	"Собеседование":         walk.RGB(240, 220, 255), // светло-пурпурный
	"Оффер":                 walk.RGB(180, 255, 180), // ярко-зеленый
	"Отказ":                 walk.RGB(255, 200, 200), // светло-красный
	"В архиве":              walk.RGB(220, 220, 220), // серый
}

// VacancyModel теперь для TableView
type VacancyModel struct {
	walk.TableModelBase
	walk.SorterBase
	walk.CellStyler
	items      []Vacancy
	sortColumn int
	sortOrder  walk.SortOrder
}

// NewVacancyModel создает новую модель для списка вакансий
func NewVacancyModel(vacancies []Vacancy) *VacancyModel {
	m := &VacancyModel{items: vacancies, sortColumn: 0, sortOrder: walk.SortAscending} // Default sort
	return m
}

// RowCount возвращает количество строк
func (m *VacancyModel) RowCount() int {
	return len(m.items)
}

// Value возвращает значение для ячейки row, col
func (m *VacancyModel) Value(row, col int) interface{} {
	item := m.items[row]
	switch col {
	case 0:
		return item.Title
	case 1:
		return item.Company
	case 2: // Новая колонка для статуса
		return item.Status
	}
	return ""
}

// Sort сортирует данные в модели
func (m *VacancyModel) Sort(col int, order walk.SortOrder) error {
	m.sortColumn = col
	m.sortOrder = order
	sort.SliceStable(m.items, func(i, j int) bool {
		return m.Less(i, j)
	})
	return m.SorterBase.Sort(col, order)
}

// Less определяет, является ли элемент i меньше элемента j
func (m *VacancyModel) Less(i, j int) bool {
	a, b := m.items[i], m.items[j]
	var less bool
	switch m.sortColumn {
	case 0:
		less = strings.ToLower(a.Title) < strings.ToLower(b.Title)
	case 1:
		less = strings.ToLower(a.Company) < strings.ToLower(b.Company)
	case 2:
		less = strings.ToLower(a.Status) < strings.ToLower(b.Status)
	default:
		less = strings.ToLower(a.Title) < strings.ToLower(b.Title) // Default to title sort if col is out of bounds
	}
	if m.sortOrder == walk.SortDescending {
		return !less
	}
	return less
}

// Swap меняет местами элементы i и j
func (m *VacancyModel) Swap(i, j int) {
	m.items[i], m.items[j] = m.items[j], m.items[i]
}

// StyleCell для реализации walk.CellStyler
func (m *VacancyModel) StyleCell(style *walk.CellStyle) {
	// Применяем стиль только к колонке "Статус" (индекс 2)
	if style.Col() != 2 || style.Row() < 0 || style.Row() >= len(m.items) {
		return
	}

	vacancyStatus := m.items[style.Row()].Status
	if color, ok := statusColors[vacancyStatus]; ok {
		style.BackgroundColor = color
	}
}

// OnlineVacancyModel for the online search results TableView
type OnlineVacancyModel struct {
	walk.TableModelBase
	items []Vacancy
}

// NewOnlineVacancyModel creates a new model for online vacancies
func NewOnlineVacancyModel() *OnlineVacancyModel {
	return &OnlineVacancyModel{items: []Vacancy{}}
}

// RowCount returns the number of rows for online vacancies
func (m *OnlineVacancyModel) RowCount() int {
	return len(m.items)
}

// Value returns the value for a cell in the online vacancies table
func (m *OnlineVacancyModel) Value(row, col int) interface{} {
	item := m.items[row]
	switch col {
	case 0:
		return item.Title
	case 1:
		return item.Company
	case 2:
		return item.SourceURL // Or other relevant field for online results
	}
	return ""
}

// AppMainWindow главная структура нашего приложения
type AppMainWindow struct {
	*walk.MainWindow
	searchEdit          *walk.LineEdit
	searchFieldCB       *walk.ComboBox
	searchLabel         *walk.Label
	statusFilterCB      *walk.ComboBox
	experienceFilterCB  *walk.ComboBox
	vacancyTable        *walk.TableView
	vacancyModel        *VacancyModel
	searchButton        *walk.PushButton
	addVacancyButton    *walk.PushButton
	editVacancyButton   *walk.PushButton
	deleteVacancyButton *walk.PushButton
	onlineSearchButton  *walk.PushButton
	resumeArchiveButton *walk.PushButton // ДОБАВЛЕНО: Кнопка архива резюме
	hSplitter           *walk.Splitter

	// Details Panel Fields
	detailsGroup           *walk.GroupBox
	detailsScrollView      *walk.ScrollView
	detailTitleLabel       *walk.Label // For "Название:"
	detailTitleDisplay     *walk.Label // To display the title (non-editable in panel)
	detailCompanyLabel     *walk.Label // For "Компания:"
	detailCompanyDisplay   *walk.Label // To display the company (non-editable in panel)
	detailStatusLabel      *walk.Label
	detailStatusCB         *walk.ComboBox // Editable
	detailExperienceLabel  *walk.Label
	detailExperienceCB     *walk.ComboBox // Editable
	detailKeywordsLabel    *walk.Label
	detailKeywordsLE       *walk.LineEdit // Editable
	detailSourceURLLabel   *walk.Label
	detailSourceURLLE      *walk.LineEdit // Editable
	detailDescriptionLabel *walk.Label
	detailDescriptionTE    *walk.TextEdit // Editable
	detailNotesLabel       *walk.Label
	detailNotesTE          *walk.TextEdit   // Editable
	saveVacancyChangesPB   *walk.PushButton // Button to save changes from details panel

	// Containers for switching views
	localVacanciesContainer *walk.Composite
	onlineResultsContainer  *walk.Composite

	// Online search results view components
	onlineResultsLabel       *walk.Label
	onlineResultsTable       *walk.TableView
	onlineVacancyModel       *OnlineVacancyModel
	backToLocalButton        *walk.PushButton
	cancelOnlineSearchButton *walk.PushButton
	addOnlineVacancyButton   *walk.PushButton

	// Канал для отмены онлайн поиска
	onlineSearchCancelChan chan struct{}

	detailResumeLabel    *walk.Label
	detailResumeDisplay  *walk.Label
	detailResumeDropArea *walk.Composite
	detailResumeOpenBtn  *walk.PushButton
	detailResumeClearBtn *walk.PushButton

	themeToggleButton *walk.PushButton
}

var possibleStatuses = []string{"Новая", "Планирую откликнуться", "Откликнулся", "Тестовое задание", "Собеседование", "Оффер", "Отказ", "В архиве"}
var possibleExperienceLevels = []string{"Не указан", "Без опыта", "Менее 1 года", "1-3 года", "3-6 лет", "Более 6 лет"}
var searchFields = []string{"Везде", "По названию", "По компании", "По описанию", "По ключевым словам", "По статусу", "По опыту"}

// Структура для диалогового окна добавления/редактирования вакансии
type AddVacancyDialog struct {
	*walk.Dialog
	titleLE         *walk.LineEdit
	companyLE       *walk.LineEdit
	descriptionTE   *walk.TextEdit
	keywordsLE      *walk.LineEdit
	sourceURLLE     *walk.LineEdit
	statusCB        *walk.ComboBox
	experienceCB    *walk.ComboBox
	notesTE         *walk.TextEdit
	acceptPB        *walk.PushButton
	cancelPB        *walk.PushButton
	vacancy         *Vacancy
	isEdit          bool
	originalTitle   string
	originalCompany string
}

// ДОБАВЛЕНО: Структура для хранения настроек приложения
type AppSettings struct {
	ThemeName string `json:"theme_name"`
}

// ДОБАВЛЕНО: Глобальные настройки
var appSettings = AppSettings{
	ThemeName: "Светлая", // По умолчанию светлая тема
}

// ДОБАВЛЕНО: Функция загрузки настроек
func loadSettings() {
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Файл настроек %s не найден, используются настройки по умолчанию", settingsFile)
			saveSettings() // Создаем файл с настройками по умолчанию
			return
		}
		log.Printf("Ошибка чтения файла настроек %s: %v", settingsFile, err)
		return
	}

	err = json.Unmarshal(data, &appSettings)
	if err != nil {
		log.Printf("Ошибка декодирования JSON из файла настроек %s: %v", settingsFile, err)
		return
	}
}

// ДОБАВЛЕНО: Функция сохранения настроек
func saveSettings() {
	data, err := json.MarshalIndent(appSettings, "", "  ")
	if err != nil {
		log.Printf("Ошибка кодирования настроек в JSON: %v", err)
		return
	}

	err = os.WriteFile(settingsFile, data, 0644)
	if err != nil {
		log.Printf("Ошибка записи файла настроек %s: %v", settingsFile, err)
	}
}

// showWelcomeDialog отображает приветственное диалоговое окно
func showWelcomeDialog(owner walk.Form) {
	var dlg *walk.Dialog

	_, err := Dialog{
		AssignTo: &dlg,
		Title:    "Добро пожаловать!",
		MinSize:  Size{Width: 380, Height: 230},
		Layout:   VBox{Margins: Margins{Top: 25, Left: 20, Right: 20, Bottom: 20}, Spacing: 10},
		Children: []Widget{
			Label{
				Text:          "Добро пожаловать в\nПоисковик Вакансий!",
				Font:          Font{PointSize: 14, Bold: true},
				TextAlignment: AlignCenter,
			},
			VSpacer{Size: 15},
			Label{
				Text:          "Это приложение поможет вам управлять\nличным списком вакансий и искать\nновые возможности онлайн.",
				TextAlignment: AlignCenter,
				Font:          Font{PointSize: 10},
			},
			VSpacer{Size: 25},
			PushButton{
				Text:    "Начать работу",
				MinSize: Size{Width: 150, Height: 0},
				OnClicked: func() {
					dlg.Accept()
				},
				Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
				Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
			},
		},
	}.Run(owner)

	if err != nil {
		log.Printf("Ошибка отображения приветственного диалога: %v", err)
	}
}

func main() {
	showWelcomeDialog(nil)
	loadVacancies()
	loadSettings() // Загружаем настройки

	app := &AppMainWindow{}
	app.vacancyModel = NewVacancyModel(allVacancies)
	app.onlineVacancyModel = NewOnlineVacancyModel()

	err := MainWindow{
		AssignTo: &app.MainWindow,
		Title:    "Поисковик Вакансий",
		MinSize:  Size{Width: 900, Height: 650},
		Size:     Size{Width: 1200, Height: 800},
		Layout:   VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			Composite{
				Layout: HBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 5}, Spacing: 8},
				Children: []Widget{
					Label{Text: "Искать в:"},
					ComboBox{
						AssignTo:     &app.searchFieldCB,
						Model:        searchFields,
						CurrentIndex: 0,
						MinSize:      Size{Width: 150, Height: 0},
						OnCurrentIndexChanged: func() {
							searchType := app.searchFieldCB.Text()
							app.searchEdit.SetVisible(false) // Сначала все скрываем
							app.statusFilterCB.SetVisible(false)
							app.experienceFilterCB.SetVisible(false)
							app.searchLabel.SetVisible(true) // Метка по умолчанию видима

							switch searchType {
							case "По статусу":
								app.searchLabel.SetText("Статус:")
								app.statusFilterCB.SetVisible(true)
								app.statusFilterCB.SetCurrentIndex(0) // Сброс на первый элемент
							case "По опыту":
								app.searchLabel.SetText("Опыт:")
								app.experienceFilterCB.SetVisible(true)
								app.experienceFilterCB.SetCurrentIndex(0) // Сброс на первый элемент
							case "Везде":
								app.searchLabel.SetText("Текст:")
								app.searchEdit.SetVisible(true)
								app.searchEdit.SetText("") // Очищаем текст
							default: // Для "По названию", "По компании" и т.д.
								app.searchLabel.SetText("Текст:")
								app.searchEdit.SetVisible(true)
								app.searchEdit.SetText("") // Очищаем текст
							}
						},
					},
					Label{AssignTo: &app.searchLabel, Text: "Текст:"},
					LineEdit{
						AssignTo:      &app.searchEdit,
						Visible:       true,
						MinSize:       Size{Width: 180, Height: 0},
						StretchFactor: 1,
					},
					ComboBox{
						AssignTo:      &app.statusFilterCB,
						Model:         possibleStatuses,
						Visible:       false,
						MinSize:       Size{Width: 180, Height: 0},
						StretchFactor: 1,
					},
					ComboBox{
						AssignTo:      &app.experienceFilterCB,
						Model:         possibleExperienceLevels,
						Visible:       false,
						MinSize:       Size{Width: 180, Height: 0},
						StretchFactor: 1,
					},
					PushButton{
						AssignTo:   &app.searchButton,
						Text:       "Найти",
						OnClicked:  app.performSearch,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
					PushButton{
						AssignTo:   &app.onlineSearchButton,
						Text:       "Онлайн поиск",
						OnClicked:  app.switchToOnlineSearchMode,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
					HSpacer{},
					PushButton{
						AssignTo:   &app.addVacancyButton,
						Text:       "Добавить",
						OnClicked:  app.showAddVacancyDialog,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
					PushButton{
						AssignTo:   &app.themeToggleButton,
						Text:       "🌙 Тёмная тема",
						OnClicked:  app.toggleTheme,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
					PushButton{
						AssignTo:   &app.editVacancyButton,
						Text:       "Изменить",
						OnClicked:  app.showEditVacancyDialog,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
						Visible:    false,
					},
					PushButton{
						AssignTo:   &app.deleteVacancyButton,
						Text:       "Удалить",
						OnClicked:  app.confirmDeleteVacancy,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
					PushButton{
						AssignTo:   &app.resumeArchiveButton,
						Text:       "Архив резюме",
						OnClicked:  app.showResumeArchive,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
				},
			},
			Composite{
				MinSize:    Size{Height: 1},
				MaxSize:    Size{Height: 1},
				Layout:     HBox{MarginsZero: true},
				Background: SolidColorBrush{Color: walk.RGB(200, 200, 200)},
			},
			VSpacer{Size: 5},
			Composite{
				AssignTo:      &app.localVacanciesContainer,
				Layout:        HBox{MarginsZero: true, SpacingZero: true},
				Visible:       true,
				StretchFactor: 1,
				Children: []Widget{
					HSplitter{
						AssignTo:      &app.hSplitter,
						StretchFactor: 1,
						HandleWidth:   5,
						Children: []Widget{
							TableView{
								AssignTo:      &app.vacancyTable,
								Model:         app.vacancyModel,
								StretchFactor: 2,
								Columns: []TableViewColumn{
									{Title: "Название", Width: 230},
									{Title: "Компания", Width: 150},
									{Title: "Статус", Width: 120},
								},
								OnCurrentIndexChanged: app.updateVacancyDetails,
								MinSize:               Size{Width: 300},
							},
							GroupBox{
								AssignTo:      &app.detailsGroup,
								Title:         "Детали вакансии",
								Layout:        VBox{MarginsZero: true, SpacingZero: true},
								StretchFactor: 1,
								MinSize:       Size{Width: 300},
								Children: []Widget{
									ScrollView{
										AssignTo:      &app.detailsScrollView,
										Layout:        VBox{Margins: Margins{Left: 9, Top: 9, Right: 9, Bottom: 9}, Spacing: 6},
										StretchFactor: 1,
										Children: []Widget{
											Label{AssignTo: &app.detailTitleLabel, Text: "Название:", Font: Font{Bold: true, PointSize: 9}},
											Label{AssignTo: &app.detailTitleDisplay, Text: "-", Font: Font{PointSize: 10, Bold: true}, TextColor: walk.RGB(0, 0, 100)},
											Label{AssignTo: &app.detailCompanyLabel, Text: "Компания:", Font: Font{Bold: true, PointSize: 9}},
											Label{AssignTo: &app.detailCompanyDisplay, Text: "-", Font: Font{PointSize: 9}},
											Label{AssignTo: &app.detailStatusLabel, Text: "Статус:", Font: Font{Bold: true, PointSize: 9}},
											ComboBox{AssignTo: &app.detailStatusCB, Model: possibleStatuses, Font: Font{PointSize: 9}},
											Label{AssignTo: &app.detailExperienceLabel, Text: "Уровень опыта:", Font: Font{Bold: true, PointSize: 9}},
											ComboBox{AssignTo: &app.detailExperienceCB, Model: possibleExperienceLevels, Font: Font{PointSize: 9}},
											Label{AssignTo: &app.detailKeywordsLabel, Text: "Ключевые слова (через запятую):", Font: Font{Bold: true, PointSize: 9}},
											LineEdit{AssignTo: &app.detailKeywordsLE, Font: Font{PointSize: 9}},
											Label{AssignTo: &app.detailSourceURLLabel, Text: "URL Источника:", Font: Font{Bold: true, PointSize: 9}},
											LineEdit{AssignTo: &app.detailSourceURLLE, Font: Font{PointSize: 9}},
											Label{AssignTo: &app.detailDescriptionLabel, Text: "Описание:", Font: Font{Bold: true, PointSize: 9}},
											TextEdit{
												AssignTo:      &app.detailDescriptionTE,
												VScroll:       true,
												MinSize:       Size{Height: 100},
												MaxSize:       Size{Height: 300},
												StretchFactor: 2,
												Font:          Font{PointSize: 9},
											},
											Label{AssignTo: &app.detailNotesLabel, Text: "Заметки:", Font: Font{Bold: true, PointSize: 9}},
											TextEdit{AssignTo: &app.detailNotesTE, MinSize: Size{0, 80}, VScroll: true, Text: "", ReadOnly: false, Font: Font{PointSize: 9}},
											Label{AssignTo: &app.detailResumeLabel, Text: "Резюме:", Font: Font{Bold: true, PointSize: 9}},
											Composite{
												AssignTo:   &app.detailResumeDropArea,
												Layout:     HBox{Margins: Margins{Top: 2, Bottom: 2}, Spacing: 5},
												MinSize:    Size{Height: 40},
												Background: SolidColorBrush{Color: walk.RGB(240, 240, 240)},
												Children: []Widget{
													Label{
														AssignTo:      &app.detailResumeDisplay,
														Text:          "Нажмите 'Выбрать' для добавления резюме",
														TextAlignment: AlignCenter,
														MinSize:       Size{Width: 200},
													},
													HSpacer{},
													PushButton{
														AssignTo:  &app.detailResumeOpenBtn,
														Text:      "Открыть",
														Enabled:   false,
														MaxSize:   Size{Width: 70},
														OnClicked: app.openResume,
														Font:      Font{Family: "Segoe UI", PointSize: 9},
													},
													PushButton{
														Text:      "Выбрать",
														MaxSize:   Size{Width: 70},
														OnClicked: app.selectResume,
														Font:      Font{Family: "Segoe UI", PointSize: 9},
													},
													PushButton{
														AssignTo:  &app.detailResumeClearBtn,
														Text:      "×",
														Enabled:   false,
														MaxSize:   Size{Width: 25},
														OnClicked: app.clearResume,
														Font:      Font{Family: "Segoe UI", PointSize: 9, Bold: true},
													},
												},
											},
											PushButton{
												AssignTo:   &app.saveVacancyChangesPB,
												Text:       "Сохранить изменения вакансии",
												OnClicked:  app.saveVacancyDetails,
												Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
												Background: SolidColorBrush{Color: walk.RGB(220, 255, 220)},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Composite{
				AssignTo:      &app.onlineResultsContainer,
				Layout:        VBox{Margins: Margins{Top: 10, Left: 10, Right: 10, Bottom: 10}, Spacing: 8},
				Visible:       false,
				StretchFactor: 1,
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							Label{
								AssignTo: &app.onlineResultsLabel,
								Text:     "Результаты онлайн-поиска:",
								Font:     Font{Bold: true, PointSize: 10},
							},
							HSpacer{},
							PushButton{
								AssignTo:   &app.cancelOnlineSearchButton,
								Text:       "Отменить поиск",
								Visible:    false,
								Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
								Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
							},
							PushButton{
								AssignTo:   &app.backToLocalButton,
								Text:       "<< Назад к локальному списку",
								Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
								Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
								OnClicked:  app.switchToLocalMode,
							},
						},
					},
					TableView{
						AssignTo: &app.onlineResultsTable,
						Model:    app.onlineVacancyModel,
						Columns: []TableViewColumn{
							{Title: "Название", Width: 220},
							{Title: "Компания", Width: 160},
							{Title: "Источник", Width: 180},
						},
						StretchFactor: 1,
						OnItemActivated: func() {
							idx := app.onlineResultsTable.CurrentIndex()
							if idx >= 0 && idx < len(app.onlineVacancyModel.items) {
								selectedOnlineVacancy := app.onlineVacancyModel.items[idx]
								vacancyCopy := selectedOnlineVacancy
								if showVacancyDialogExt(app, &vacancyCopy, false, true) {
									app.onlineVacancyModel.items = append(app.onlineVacancyModel.items[:idx], app.onlineVacancyModel.items[idx+1:]...)
									app.onlineVacancyModel.PublishRowsReset()
									app.performSearch()
								}
							}
						},
					},
					PushButton{
						AssignTo:   &app.addOnlineVacancyButton,
						Text:       "Добавить выбранное в локальный список",
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
						OnClicked: func() {
							idx := app.onlineResultsTable.CurrentIndex()
							if idx < 0 || idx >= len(app.onlineVacancyModel.items) {
								walk.MsgBox(app.MainWindow, "Подсказка", "Пожалуйста, сначала выберите вакансию из списка выше.", walk.MsgBoxIconInformation)
								return
							}
							selectedOnlineVacancy := app.onlineVacancyModel.items[idx]
							vacancyCopy := selectedOnlineVacancy
							if showVacancyDialogExt(app, &vacancyCopy, false, true) {
								app.onlineVacancyModel.items = append(app.onlineVacancyModel.items[:idx], app.onlineVacancyModel.items[idx+1:]...)
								app.onlineVacancyModel.PublishRowsReset()
								app.performSearch()
							}
						},
					},
				},
			},
		},
	}.Create()

	if err != nil {
		log.Fatal(err)
	}

	// Сначала инициализируем таблицу
	if app.vacancyTable != nil {
		app.vacancyTable.SetAlternatingRowBG(true)
		app.vacancyModel.Sort(app.vacancyModel.sortColumn, app.vacancyModel.sortOrder)
	}

	// Затем применяем тему
	initialTheme := lightTheme
	if appSettings.ThemeName == "Тёмная" {
		initialTheme = darkTheme
		if app.themeToggleButton != nil {
			app.themeToggleButton.SetText("☀ Светлая тема")
		}
	}
	app.applyTheme(initialTheme)

	app.vacancyModel.PublishRowsReset()
	app.updateVacancyDetails()

	app.MainWindow.Run()
}

// performSearch обрабатывает нажатие кнопки "Поиск"
func (app *AppMainWindow) performSearch() {
	allVacanciesMutex.Lock()
	currentSearchVacancies := make([]Vacancy, len(allVacancies))
	copy(currentSearchVacancies, allVacancies)
	allVacanciesMutex.Unlock()

	var searchTerm string
	searchInFieldIndex := app.searchFieldCB.CurrentIndex()
	searchInField := "Везде"
	if searchInFieldIndex >= 0 && searchInFieldIndex < len(searchFields) {
		searchInField = searchFields[searchInFieldIndex]
	}

	// Получаем searchTerm в зависимости от выбранного поля поиска
	switch searchInField {
	case "По статусу":
		searchTerm = app.statusFilterCB.Text()
	case "По опыту":
		searchTerm = app.experienceFilterCB.Text()
	default:
		searchTerm = app.searchEdit.Text()
	}
	searchTerm = strings.ToLower(searchTerm)

	// Логика фильтрации (остается почти такой же, но использует уже подготовленный searchTerm)
	if searchTerm == "" && searchInField != "По опыту" && searchInField != "По статусу" {
		app.vacancyModel.items = currentSearchVacancies
	} else {
		filtered := []Vacancy{}
		for _, v := range currentSearchVacancies {
			found := false
			matchField := func(fieldValue string) bool {
				// Для точного совпадения по статусу и опыту из ComboBox, если они выбраны
				if searchInField == "По статусу" || searchInField == "По опыту" {
					return strings.EqualFold(fieldValue, searchTerm) // Точное совпадение (без учета регистра)
				}
				return strings.Contains(strings.ToLower(fieldValue), searchTerm) // Для остальных - поиск подстроки
			}

			switch searchInField {
			case "По названию":
				found = matchField(v.Title)
			case "По компании":
				found = matchField(v.Company)
			case "По описанию":
				found = matchField(v.Description)
			case "По ключевым словам":
				// searchTerm здесь - это то, что введено в searchEdit
				for _, kw := range v.Keywords {
					if strings.Contains(strings.ToLower(kw), searchTerm) { // Всегда поиск подстроки для ключевых слов
						found = true
						break
					}
				}
			case "По статусу":
				found = matchField(v.Status) // searchTerm берется из statusFilterCB
			case "По опыту":
				found = matchField(v.ExperienceLevel) // searchTerm берется из experienceFilterCB
			default: // "Везде"
				// searchTerm здесь - это то, что введено в searchEdit
				if strings.Contains(strings.ToLower(v.Title), searchTerm) ||
					strings.Contains(strings.ToLower(v.Company), searchTerm) ||
					strings.Contains(strings.ToLower(v.Description), searchTerm) ||
					strings.Contains(strings.ToLower(v.Status), searchTerm) ||
					strings.Contains(strings.ToLower(v.ExperienceLevel), searchTerm) {
					found = true
				} else {
					for _, kw := range v.Keywords {
						if strings.Contains(strings.ToLower(kw), searchTerm) {
							found = true
							break
						}
					}
				}
			}

			if found {
				filtered = append(filtered, v)
			}
		}
		app.vacancyModel.items = filtered
	}

	app.vacancyModel.Sort(app.vacancyModel.sortColumn, app.vacancyModel.sortOrder)
	app.vacancyModel.PublishRowsReset()
	app.updateVacancyDetails()
}

// showAddVacancyDialog отображает диалоговое окно для добавления новой вакансии
func (app *AppMainWindow) showAddVacancyDialog() {
	v := Vacancy{}
	showVacancyDialogExt(app, &v, false, false)
	app.performSearch() // Обновляем после закрытия диалога
}

// showEditVacancyDialog отображает диалоговое окно для редактирования выбранной вакансии
func (app *AppMainWindow) showEditVacancyDialog() {
	idx := app.vacancyTable.CurrentIndex()
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		walk.MsgBox(app.MainWindow, "Ошибка", "Пожалуйста, выберите вакансию для редактирования.", walk.MsgBoxIconWarning)
		return
	}
	// Нам нужно найти оригинальную вакансию в allVacancies, чтобы редактировать ее, а не копию из отфильтрованного списка
	originalIndex := app.findVacancyIndexInAllExt(app.vacancyModel.items[idx].Title, app.vacancyModel.items[idx].Company)
	if originalIndex == -1 {
		walk.MsgBox(app.MainWindow, "Ошибка", "Не удалось найти оригинальную вакансию для редактирования.", walk.MsgBoxIconError)
		return
	}
	vacancyToEdit := allVacancies[originalIndex] // Получаем копию для редактирования

	if showVacancyDialogExt(app, &vacancyToEdit, true, false) {
		// Если пользователь сохранил изменения, вакансия в allVacancies[originalIndex] уже обновлена в showVacancyDialogExt
		// через savedVacancy и allVacancies[originalIndex] = savedVacancy
		// saveVacancies() также был вызван в showVacancyDialogExt
		app.performSearch() // Обновляем TableView
	}
}

// findVacancyIndexInAllExt ищет вакансию по Title и Company
func (app *AppMainWindow) findVacancyIndexInAllExt(title, company string) int {
	for i, v := range allVacancies {
		if strings.EqualFold(v.Title, title) && strings.EqualFold(v.Company, company) { // Case-insensitive search
			return i
		}
	}
	return -1
}

// showVacancyDialogExt это расширенная версия showVacancyDialog, которая возвращает bool
// True если вакансия была сохранена (пользователь нажал "Добавить в локальные" или "Сохранить")
// False если пользователь нажал "Отмена" или закрыл диалог
func showVacancyDialogExt(app *AppMainWindow, currentVacancy *Vacancy, isEdit bool, isOnlineSearch bool) bool {
	dlg := &AddVacancyDialog{vacancy: currentVacancy, isEdit: isEdit}
	var dialogTitle string
	buttonText := "Сохранить"

	if isEdit {
		dialogTitle = "Редактировать вакансию"
		dlg.originalTitle = currentVacancy.Title
		dlg.originalCompany = currentVacancy.Company
	} else if isOnlineSearch {
		dialogTitle = "Детали вакансии (онлайн)"
		buttonText = "Добавить в локальный список"
	} else {
		dialogTitle = "Добавить новую вакансию"
	}

	fieldsReadOnly := isOnlineSearch
	sourceURLReadOnly := true

	initialStatusIndex := 0
	if currentVacancy.Status != "" {
		for i, s := range possibleStatuses {
			if s == currentVacancy.Status {
				initialStatusIndex = i
				break
			}
		}
	} else if !isEdit {
		currentVacancy.Status = possibleStatuses[0]
	}

	// ДОБАВЛЕНО: Логика для начального значения ExperienceLevel
	initialExperienceIndex := 0
	if currentVacancy.ExperienceLevel != "" {
		for i, el := range possibleExperienceLevels {
			if el == currentVacancy.ExperienceLevel {
				initialExperienceIndex = i
				break
			}
		}
	} else {
		currentVacancy.ExperienceLevel = possibleExperienceLevels[0] // "Не указан" по умолчанию
	}

	if !isEdit && !isOnlineSearch {
		fieldsReadOnly = false
		sourceURLReadOnly = false
	}

	var accepted bool
	if _, errDialog := (Dialog{
		AssignTo:      &dlg.Dialog,
		Title:         dialogTitle,
		DefaultButton: &dlg.acceptPB,
		CancelButton:  &dlg.cancelPB,
		MinSize:       Size{Width: 500, Height: 700}, // Увеличена высота для нового поля заметки
		Layout:        VBox{Margins: Margins{Top: 10, Left: 10, Right: 10, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			Label{Text: "Название вакансии:", Font: Font{Bold: true, PointSize: 9}},
			LineEdit{AssignTo: &dlg.titleLE, Text: dlg.vacancy.Title, ReadOnly: fieldsReadOnly, Font: Font{PointSize: 9}},
			Label{Text: "Компания:", Font: Font{Bold: true, PointSize: 9}},
			LineEdit{AssignTo: &dlg.companyLE, Text: dlg.vacancy.Company, ReadOnly: fieldsReadOnly, Font: Font{PointSize: 9}},
			Label{Text: "Статус:", Font: Font{Bold: true, PointSize: 9}},
			ComboBox{
				AssignTo:     &dlg.statusCB,
				Model:        possibleStatuses,
				CurrentIndex: initialStatusIndex,
				Font:         Font{PointSize: 9},
			},
			// ДОБАВЛЕНО: ComboBox для Уровня опыта
			Label{Text: "Уровень опыта:", Font: Font{Bold: true, PointSize: 9}},
			ComboBox{
				AssignTo:     &dlg.experienceCB,
				Model:        possibleExperienceLevels,
				CurrentIndex: initialExperienceIndex,
				Font:         Font{PointSize: 9},
			},
			Label{Text: "Ключевые слова (через запятую):", Font: Font{Bold: true, PointSize: 9}},
			LineEdit{AssignTo: &dlg.keywordsLE, Text: strings.Join(dlg.vacancy.Keywords, ", "), ReadOnly: false, Font: Font{PointSize: 9}},
			Label{Text: "URL Источника:", Font: Font{Bold: true, PointSize: 9}},
			LineEdit{AssignTo: &dlg.sourceURLLE, Text: dlg.vacancy.SourceURL, ReadOnly: sourceURLReadOnly, Font: Font{PointSize: 9}},
			Label{Text: "Описание:", Font: Font{Bold: true, PointSize: 9}},
			TextEdit{AssignTo: &dlg.descriptionTE, MinSize: Size{0, 100}, VScroll: true, Text: dlg.vacancy.Description, ReadOnly: fieldsReadOnly, Font: Font{PointSize: 9}},
			Label{Text: "Заметки:", Font: Font{Bold: true, PointSize: 9}},
			TextEdit{AssignTo: &dlg.notesTE, MinSize: Size{0, 80}, VScroll: true, Text: dlg.vacancy.Notes, ReadOnly: false, Font: Font{PointSize: 9}},
			Composite{
				Layout: HBox{Margins: Margins{Top: 15}, SpacingZero: true},
				Children: []Widget{
					HSpacer{StretchFactor: 1},
					PushButton{
						AssignTo:   &dlg.acceptPB,
						Text:       buttonText,
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
						OnClicked: func() {
							savedVacancy := Vacancy{}
							savedVacancy.Title = strings.TrimSpace(dlg.titleLE.Text())
							savedVacancy.Company = strings.TrimSpace(dlg.companyLE.Text())
							savedVacancy.Description = strings.TrimSpace(dlg.descriptionTE.Text())
							keywordsStr := dlg.keywordsLE.Text()
							savedVacancy.Keywords = []string{}
							if strings.TrimSpace(keywordsStr) != "" {
								for _, kw := range strings.Split(keywordsStr, ",") {
									trimmedKw := strings.TrimSpace(kw)
									if trimmedKw != "" {
										savedVacancy.Keywords = append(savedVacancy.Keywords, trimmedKw)
									}
								}
							}
							savedVacancy.SourceURL = strings.TrimSpace(dlg.sourceURLLE.Text())
							savedVacancy.Status = dlg.statusCB.Text()
							savedVacancy.ExperienceLevel = dlg.experienceCB.Text()     // ДОБАВЛЕНО: Сохранение уровня опыта
							savedVacancy.Notes = strings.TrimSpace(dlg.notesTE.Text()) // ДОБАВЛЕНО: Сохранение заметок

							if savedVacancy.Title == "" {
								walk.MsgBox(dlg.Dialog, "Ошибка", "Название вакансии не может быть пустым.", walk.MsgBoxIconWarning)
								return
							}

							if dlg.isEdit && !isOnlineSearch {
								originalIndex := app.findVacancyIndexInAllExt(dlg.originalTitle, dlg.originalCompany)
								if originalIndex != -1 {
									allVacancies[originalIndex] = savedVacancy
								} else {
									walk.MsgBox(app.MainWindow, "Ошибка", "Не удалось найти оригинальную вакансию для обновления.", walk.MsgBoxIconError)
									dlg.Cancel()
									return
								}
							} else {
								if app.findVacancyIndexInAllExt(savedVacancy.Title, savedVacancy.Company) != -1 {
									walk.MsgBox(dlg.Dialog, "Информация", "Эта вакансия уже есть в вашем локальном списке.", walk.MsgBoxIconInformation)
									return
								}
								allVacancies = append(allVacancies, savedVacancy)
							}
							saveVacancies()
							accepted = true
							dlg.Accept()
						},
					},
					PushButton{
						AssignTo:   &dlg.cancelPB,
						Text:       "Отмена",
						OnClicked:  func() { dlg.Cancel() },
						Background: SolidColorBrush{Color: walk.RGB(235, 235, 235)},
						Font:       Font{Family: "Segoe UI", PointSize: 10, Bold: true},
					},
				},
			},
		},
	}).Run(app.MainWindow); errDialog != nil {
		log.Print("Dialog run error: ", errDialog)
	}
	return accepted
}

// confirmDeleteVacancy запрашивает подтверждение и удаляет выбранную вакансию
func (app *AppMainWindow) confirmDeleteVacancy() {
	idx := app.vacancyTable.CurrentIndex() // Используем vacancyTable
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		walk.MsgBox(app.MainWindow, "Ошибка", "Пожалуйста, выберите вакансию для удаления.", walk.MsgBoxIconWarning)
		return
	}

	selectedVacancyInModel := app.vacancyModel.items[idx]

	if walk.DlgCmdYes != walk.MsgBox(app.MainWindow, "Подтверждение удаления", "Вы уверены, что хотите удалить вакансию '"+selectedVacancyInModel.Title+"'?", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) {
		return
	}

	originalIndexInAll := app.findVacancyIndexInAllExt(selectedVacancyInModel.Title, selectedVacancyInModel.Company)
	if originalIndexInAll == -1 {
		log.Printf("Ошибка: не удалось найти вакансию '%s' в основном списке для удаления.", selectedVacancyInModel.Title)
		walk.MsgBox(app.MainWindow, "Ошибка", "Произошла внутренняя ошибка при попытке удалить вакансию.", walk.MsgBoxIconError)
		return
	}

	allVacancies = append(allVacancies[:originalIndexInAll], allVacancies[originalIndexInAll+1:]...)

	saveVacancies()
	app.performSearch()
	// app.updateVacancyDetails() // performSearch уже это делает

	walk.MsgBox(app.MainWindow, "Удалено", "Вакансия '"+selectedVacancyInModel.Title+"' была успешно удалена.", walk.MsgBoxIconInformation)
}

// updateVacancyDetails обновляет поля с деталями выбранной вакансии
func (app *AppMainWindow) updateVacancyDetails() {
	idx := -1
	if app.vacancyTable != nil {
		idx = app.vacancyTable.CurrentIndex()
	}

	// Создаем функцию для обновления UI, которую будем вызывать через Synchronize
	updateUI := func(vacancy Vacancy, hasSelection bool) {
		if !hasSelection {
			// Clear details panel and disable save button if nothing is selected
			if app.detailTitleDisplay != nil {
				app.detailTitleDisplay.SetText("-")
			}
			if app.detailCompanyDisplay != nil {
				app.detailCompanyDisplay.SetText("-")
			}
			if app.detailStatusCB != nil {
				app.detailStatusCB.SetCurrentIndex(-1)
				app.detailStatusCB.SetEnabled(false)
			}
			if app.detailExperienceCB != nil {
				app.detailExperienceCB.SetCurrentIndex(-1)
				app.detailExperienceCB.SetEnabled(false)
			}
			if app.detailKeywordsLE != nil {
				app.detailKeywordsLE.SetText("")
				app.detailKeywordsLE.SetEnabled(false)
			}
			if app.detailSourceURLLE != nil {
				app.detailSourceURLLE.SetText("")
				app.detailSourceURLLE.SetEnabled(false)
			}
			if app.detailDescriptionTE != nil {
				app.detailDescriptionTE.SetText("")
				app.detailDescriptionTE.SetEnabled(false)
			}
			if app.detailNotesTE != nil {
				app.detailNotesTE.SetText("")
				app.detailNotesTE.SetEnabled(false)
			}
			if app.saveVacancyChangesPB != nil {
				app.saveVacancyChangesPB.SetEnabled(false)
			}
			if app.detailResumeDisplay != nil {
				app.detailResumeDisplay.SetText("Нет прикрепленного резюме")
			}
			if app.detailResumeOpenBtn != nil {
				app.detailResumeOpenBtn.SetEnabled(false)
			}
			if app.detailResumeClearBtn != nil {
				app.detailResumeClearBtn.SetEnabled(false)
			}
			return
		}

		// Update fields with selected vacancy data
		if app.detailTitleDisplay != nil {
			app.detailTitleDisplay.SetText(vacancy.Title)
		}
		if app.detailCompanyDisplay != nil {
			app.detailCompanyDisplay.SetText(vacancy.Company)
		}

		if app.detailStatusCB != nil {
			app.detailStatusCB.SetEnabled(true)
			currentStatusIdx := -1
			for i, s := range possibleStatuses {
				if s == vacancy.Status {
					currentStatusIdx = i
					break
				}
			}
			app.detailStatusCB.SetCurrentIndex(currentStatusIdx)
			if currentStatusIdx == -1 && vacancy.Status == "" && len(possibleStatuses) > 0 {
				app.detailStatusCB.SetCurrentIndex(0)
			}
		}

		if app.detailExperienceCB != nil {
			app.detailExperienceCB.SetEnabled(true)
			currentExpIdx := -1
			for i, el := range possibleExperienceLevels {
				if el == vacancy.ExperienceLevel {
					currentExpIdx = i
					break
				}
			}
			app.detailExperienceCB.SetCurrentIndex(currentExpIdx)
			if currentExpIdx == -1 && vacancy.ExperienceLevel == "" && len(possibleExperienceLevels) > 0 {
				app.detailExperienceCB.SetCurrentIndex(0)
			}
		}

		if app.detailKeywordsLE != nil {
			app.detailKeywordsLE.SetText(strings.Join(vacancy.Keywords, ", "))
			app.detailKeywordsLE.SetEnabled(true)
		}
		if app.detailSourceURLLE != nil {
			app.detailSourceURLLE.SetText(vacancy.SourceURL)
			app.detailSourceURLLE.SetEnabled(true)
		}
		if app.detailDescriptionTE != nil {
			app.detailDescriptionTE.SetText(vacancy.Description)
			app.detailDescriptionTE.SetEnabled(true)
		}
		if app.detailNotesTE != nil {
			app.detailNotesTE.SetText(vacancy.Notes)
			app.detailNotesTE.SetEnabled(true)
		}
		if app.saveVacancyChangesPB != nil {
			app.saveVacancyChangesPB.SetEnabled(true)
		}

		// Обновляем информацию о резюме
		if app.detailResumeDisplay != nil {
			if vacancy.ResumeFileName != "" {
				app.detailResumeDisplay.SetText(vacancy.ResumeFileName)
				if app.detailResumeOpenBtn != nil {
					app.detailResumeOpenBtn.SetEnabled(true)
				}
				if app.detailResumeClearBtn != nil {
					app.detailResumeClearBtn.SetEnabled(true)
				}
			} else {
				app.detailResumeDisplay.SetText("Перетащите файл резюме сюда")
				if app.detailResumeOpenBtn != nil {
					app.detailResumeOpenBtn.SetEnabled(false)
				}
				if app.detailResumeClearBtn != nil {
					app.detailResumeClearBtn.SetEnabled(false)
				}
			}
		}
	}

	// Определяем, есть ли выделение и какие данные показывать
	var vacancy Vacancy
	hasSelection := false
	if idx >= 0 && idx < len(app.vacancyModel.items) {
		vacancy = app.vacancyModel.items[idx]
		hasSelection = true
	}

	// Вызываем обновление UI через Synchronize
	if app.MainWindow != nil {
		app.MainWindow.Synchronize(func() {
			updateUI(vacancy, hasSelection)

			// Обновляем layout всей панели деталей
			if app.detailsGroup != nil {
				app.detailsGroup.SetVisible(false)
				app.detailsGroup.SetVisible(true)

				// Принудительно обновляем layout всего окна
				app.MainWindow.SetBounds(app.MainWindow.Bounds())
			}
		})
	}
}

// saveVacancyDetails сохраняет изменения, сделанные в панели деталей
func (app *AppMainWindow) saveVacancyDetails() {
	idx := app.vacancyTable.CurrentIndex()
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		app.MainWindow.Synchronize(func() {
			walk.MsgBox(app.MainWindow, "Внимание", "Нет выбранной вакансии для сохранения.", walk.MsgBoxIconWarning)
		})
		return
	}

	vacancyInView := app.vacancyModel.items[idx]

	allVacanciesMutex.Lock()
	originalIndexInAll := -1
	for i, v := range allVacancies {
		if v.Title == vacancyInView.Title && v.Company == vacancyInView.Company {
			originalIndexInAll = i
			break
		}
	}

	if originalIndexInAll == -1 {
		allVacanciesMutex.Unlock()
		app.MainWindow.Synchronize(func() {
			walk.MsgBox(app.MainWindow, "Ошибка", "Не удалось найти оригинальную вакансию для обновления.", walk.MsgBoxIconError)
		})
		return
	}

	updatedVacancy := allVacancies[originalIndexInAll]
	changed := false

	if app.detailStatusCB != nil {
		newStatus := app.detailStatusCB.Text()
		if updatedVacancy.Status != newStatus {
			updatedVacancy.Status = newStatus
			changed = true
		}
	}
	if app.detailExperienceCB != nil {
		newExperience := app.detailExperienceCB.Text()
		if updatedVacancy.ExperienceLevel != newExperience {
			updatedVacancy.ExperienceLevel = newExperience
			changed = true
		}
	}
	if app.detailKeywordsLE != nil {
		newKeywordsStr := app.detailKeywordsLE.Text()
		newKeywords := []string{}
		if strings.TrimSpace(newKeywordsStr) != "" {
			for _, kw := range strings.Split(newKeywordsStr, ",") {
				trimmedKw := strings.TrimSpace(kw)
				if trimmedKw != "" {
					newKeywords = append(newKeywords, trimmedKw)
				}
			}
		}
		if !equalStringSlices(updatedVacancy.Keywords, newKeywords) {
			updatedVacancy.Keywords = newKeywords
			changed = true
		}
	}
	if app.detailSourceURLLE != nil {
		newSourceURL := app.detailSourceURLLE.Text()
		if updatedVacancy.SourceURL != newSourceURL {
			updatedVacancy.SourceURL = newSourceURL
			changed = true
		}
	}
	if app.detailDescriptionTE != nil {
		newDescription := app.detailDescriptionTE.Text()
		if updatedVacancy.Description != newDescription {
			updatedVacancy.Description = newDescription
			changed = true
		}
	}
	if app.detailNotesTE != nil {
		newNotes := app.detailNotesTE.Text()
		if updatedVacancy.Notes != newNotes {
			updatedVacancy.Notes = newNotes
			changed = true
		}
	}

	if changed {
		allVacancies[originalIndexInAll] = updatedVacancy
		// Save to file in background
		go saveVacancies()
		log.Printf("Вакансия '%s' обновлена через панель деталей.", updatedVacancy.Title)
		app.MainWindow.Synchronize(func() {
			walk.MsgBox(app.MainWindow, "Сохранено", "Изменения для вакансии '"+updatedVacancy.Title+"' сохранены.", walk.MsgBoxIconInformation)
		})
	} else {
		app.MainWindow.Synchronize(func() {
			walk.MsgBox(app.MainWindow, "Информация", "Нет изменений для сохранения.", walk.MsgBoxIconInformation)
		})
	}
	allVacanciesMutex.Unlock()

	// PerformSearch already calls updateVacancyDetails, which is now synchronized.
	app.performSearch()
}

// equalStringSlices проверяет, равны ли два строковых слайса (порядок важен)
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func loadVacancies() {
	data, err := os.ReadFile(vacanciesFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Файл %s не найден, создаем с примерами.", vacanciesFile)
			allVacanciesMutex.Lock()
			allVacancies = []Vacancy{
				{Title: "Разработчик Go (пример)", Company: "Tech Solutions", Description: "Требуется опытный Go разработчик.", Keywords: []string{"golang", "backend"}, Status: "Новая", ExperienceLevel: "3-6 лет", Notes: "Очень интересная вакансия, гибкий график."},
				{Title: "Frontend Developer (пример)", Company: "Web Innovators", Description: "Ищем frontend разработчика.", Keywords: []string{"javascript", "react"}, Status: "Новая", ExperienceLevel: "1-3 года", Notes: "Нужно портфолио."},
				{Title: "Junior QA Engineer (пример)", Company: "QA Experts", Description: "Ищем начинающего тестировщика.", Keywords: []string{"qa", "testing"}, Status: "Планирую откликнуться", ExperienceLevel: "Без опыта", Notes: "Откликнуться до конца недели."},
			}
			allVacanciesMutex.Unlock()
			saveVacancies()
			return
		}
		log.Printf("Ошибка чтения файла %s: %v", vacanciesFile, err)
		return
	}

	allVacanciesMutex.Lock()
	defer allVacanciesMutex.Unlock()
	err = json.Unmarshal(data, &allVacancies)
	if err != nil {
		log.Printf("Ошибка декодирования JSON из файла %s: %v", vacanciesFile, err)
		allVacancies = []Vacancy{}
		return
	}
	log.Printf("Загружено %d вакансий из файла %s", len(allVacancies), vacanciesFile)
}

// saveVacancies сохраняет текущий список вакансий в файл vacancies.json
func saveVacancies() {
	allVacanciesMutex.Lock()
	defer allVacanciesMutex.Unlock()

	data, err := json.MarshalIndent(allVacancies, "", "  ")
	if err != nil {
		log.Printf("Ошибка кодирования вакансий в JSON: %v", err)
		return
	}

	err = os.WriteFile(vacanciesFile, data, 0644)
	if err != nil {
		log.Printf("Ошибка записи файла %s: %v", vacanciesFile, err)
	}
	log.Printf("Сохранено %d вакансий в файл %s", len(allVacancies), vacanciesFile)
}

// Новые структуры для Jooble API
type JoobleRequest struct {
	Keywords string `json:"keywords"`
	Location string `json:"location,omitempty"`
	Page     int    `json:"page,omitempty"`
}

// ИСПРАВЛЕНО: Восстановление структуры JoobleJob
type JoobleJob struct {
	Title    string      `json:"title"`
	Location string      `json:"location"`
	Snippet  string      `json:"snippet"`
	Salary   string      `json:"salary"`
	Source   string      `json:"source"`
	Type     string      `json:"type"`
	Link     string      `json:"link"`
	Company  string      `json:"company"`
	Updated  string      `json:"updated"`
	ID       interface{} `json:"id"`
}

// ИСПРАВЛЕНО: Восстановление JoobleResponse
type JoobleResponse struct {
	TotalCount int          `json:"totalCount"`
	Jobs       []JoobleJob  `json:"jobs"`
	Error      *JoobleError `json:"error,omitempty"`
}

// ИСПРАВЛЕНО: Восстановление JoobleError
type JoobleError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ИСПРАВЛЕНО: Восстановление функции searchVacanciesJooble
func searchVacanciesJooble(keywords, location string, ch chan struct{}) ([]Vacancy, error) {
	apiURL := "https://jooble.org/api/"
	joobleReq := JoobleRequest{
		Keywords: keywords,
		Location: location,
		Page:     1,
	}

	jsonData, err := json.Marshal(joobleReq)
	if err != nil {
		return nil, fmt.Errorf("ошибка кодирования запроса в JSON: %w", err)
	}

	// Создаем контекст для отмены HTTP-запроса
	ctx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest() // Убедимся, что cancelRequest вызывается при выходе из функции

	// Goroutine для прослушивания канала отмены от UI и отмены HTTP-контекста
	go func() {
		select {
		case <-ch: // Получен сигнал отмены из UI
			cancelRequest() // Отменяем HTTP-запрос
		case <-ctx.Done(): // Контекст HTTP-запроса уже завершен (например, по таймауту или другой причине)
			// Ничего не делаем, запрос уже завершился или был отменен
		}
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+joobleAPIKey, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("ошибка создания HTTP запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		// Проверяем, была ли ошибка вызвана отменой контекста
		select {
		case <-ch: // Канал отмены из UI закрыт
			return nil, fmt.Errorf("поиск отменен пользователем (сигнал из UI)")
		default:
			if ctx.Err() == context.Canceled {
				return nil, fmt.Errorf("поиск отменен пользователем (контекст HTTP)")
			}
			return nil, fmt.Errorf("ошибка выполнения HTTP запроса: %w", err)
		}
	}
	defer resp.Body.Close()

	// Проверка на отмену перед чтением тела (на всякий случай, если Do() не вернул ошибку сразу)
	select {
	case <-ch:
		return nil, fmt.Errorf("поиск отменен пользователем перед чтением ответа")
	default:
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения тела ответа: %w", err)
	}

	// Еще одна проверка на отмену
	select {
	case <-ch:
		return nil, fmt.Errorf("поиск отменен пользователем перед обработкой ответа")
	default:
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ошибка API Jooble (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var joobleResp JoobleResponse
	err = json.Unmarshal(body, &joobleResp)
	if err != nil {
		var joobleErr JoobleError
		if json.Unmarshal(body, &joobleErr) == nil && joobleErr.Message != "" {
			return nil, fmt.Errorf("ошибка API Jooble: %s (код: %d)", joobleErr.Message, joobleErr.Code)
		}
		return nil, fmt.Errorf("ошибка декодирования JSON ответа от Jooble: %w. Ответ: %s", err, string(body))
	}

	if joobleResp.Error != nil {
		return nil, fmt.Errorf("API Jooble вернуло ошибку: %s (код: %d)", joobleResp.Error.Message, joobleResp.Error.Code)
	}

	var vacancies []Vacancy
	for _, job := range joobleResp.Jobs {
		// Проверка на отмену в цикле, если вакансий много
		select {
		case <-ch:
			return nil, fmt.Errorf("поиск отменен пользователем во время обработки результатов")
		default:
		}
		if job.Title == "" || job.Link == "" {
			log.Printf("Пропущена вакансия от Jooble из-за отсутствия Title или Link: %+v", job)
			continue
		}
		vacancies = append(vacancies, Vacancy{
			Title:           job.Title,
			Company:         job.Company,
			Description:     job.Snippet,
			Keywords:        []string{},
			SourceURL:       job.Link,
			Status:          possibleStatuses[0],         // "Новая"
			ExperienceLevel: possibleExperienceLevels[0], // ДОБАВЛЕНО: "Не указан" для вакансий Jooble
			Notes:           "",                          // ДОБАВЛЕНО: Пустые заметки для онлайн вакансий
		})
	}

	return vacancies, nil
}

// ИСПРАВЛЕНО: Восстановление метода switchToLocalMode
func (app *AppMainWindow) switchToLocalMode() {
	if app.localVacanciesContainer == nil || app.onlineResultsContainer == nil {
		log.Println("switchToLocalMode: один из контейнеров не инициализирован")
		return
	}
	app.localVacanciesContainer.SetVisible(true)
	app.onlineResultsContainer.SetVisible(false)

	if app.cancelOnlineSearchButton != nil { // Скрываем кнопку отмены
		app.cancelOnlineSearchButton.SetVisible(false)
	}

	// Включаем кнопки для локальных операций
	if app.addVacancyButton != nil {
		app.addVacancyButton.SetEnabled(true)
	}
	if app.editVacancyButton != nil {
		app.editVacancyButton.SetEnabled(true)
	}
	if app.deleteVacancyButton != nil {
		app.deleteVacancyButton.SetEnabled(true)
	}
	if app.searchEdit != nil {
		app.searchEdit.SetEnabled(true)
	}
	if app.searchFieldCB != nil {
		app.searchFieldCB.SetEnabled(true)
	}
	if app.searchButton != nil {
		app.searchButton.SetEnabled(true)
	} // Убедимся, что кнопка поиска тоже включается
	if app.onlineSearchButton != nil {
		app.onlineSearchButton.SetEnabled(true)
	} // И кнопка онлайн-поиска

	app.performSearch()
}

// ИСПРАВЛЕНО: Восстановление метода switchToOnlineSearchMode
func (app *AppMainWindow) switchToOnlineSearchMode() {
	searchTerm := app.searchEdit.Text()
	if searchTerm == "" {
		walk.MsgBox(app.MainWindow, "Онлайн поиск", "Пожалуйста, введите текст для поиска.", walk.MsgBoxIconInformation)
		return
	}

	if app.localVacanciesContainer == nil || app.onlineResultsContainer == nil || app.cancelOnlineSearchButton == nil || app.backToLocalButton == nil {
		log.Println("switchToOnlineSearchMode: один из ключевых компонентов UI не инициализирован")
		return
	}
	app.localVacanciesContainer.SetVisible(false)
	app.onlineResultsContainer.SetVisible(true)

	app.onlineSearchCancelChan = make(chan struct{})
	cancelChan := app.onlineSearchCancelChan

	app.cancelOnlineSearchButton.SetVisible(true)
	app.cancelOnlineSearchButton.SetEnabled(true)
	app.cancelOnlineSearchButton.SetText("Отменить поиск")

	app.cancelOnlineSearchButton.Clicked().Attach(func() {
		select {
		case <-cancelChan:
		default:
			close(cancelChan)
		}
		app.cancelOnlineSearchButton.SetEnabled(false)
		app.cancelOnlineSearchButton.SetText("Отменяется...")
	})

	app.backToLocalButton.SetEnabled(true)
	app.backToLocalButton.Clicked().Attach(func() {
		select {
		case <-cancelChan:
		default:
			close(cancelChan)
		}
		app.switchToLocalMode()
	})

	if app.addVacancyButton != nil {
		app.addVacancyButton.SetEnabled(false)
	}
	if app.editVacancyButton != nil {
		app.editVacancyButton.SetEnabled(false)
	}
	if app.deleteVacancyButton != nil {
		app.deleteVacancyButton.SetEnabled(false)
	}
	if app.searchButton != nil {
		app.searchButton.SetEnabled(false)
	}
	if app.onlineSearchButton != nil {
		app.onlineSearchButton.SetEnabled(false)
	}

	app.onlineVacancyModel.items = []Vacancy{}
	app.onlineVacancyModel.PublishRowsReset()
	app.onlineResultsLabel.SetText("Идет поиск онлайн... Пожалуйста, подождите.")

	go func(currentSearchTerm string, ch chan struct{}) {
		joobleVacancies, err := searchVacanciesJooble(currentSearchTerm, "", ch)

		select {
		case <-ch:
			app.MainWindow.Synchronize(func() {
				app.onlineResultsLabel.SetText(fmt.Sprintf("Онлайн поиск по запросу '%s' отменен.", currentSearchTerm))
				if app.cancelOnlineSearchButton != nil {
					app.cancelOnlineSearchButton.SetVisible(false)
				}
				if app.onlineSearchButton != nil {
					app.onlineSearchButton.SetEnabled(true)
				}
				if app.searchButton != nil {
					app.searchButton.SetEnabled(true)
				}
			})
			return
		default:
		}

		app.MainWindow.Synchronize(func() {
			if app.cancelOnlineSearchButton != nil {
				app.cancelOnlineSearchButton.SetVisible(false)
			}
			if app.onlineSearchButton != nil {
				app.onlineSearchButton.SetEnabled(true)
			}
			if app.searchButton != nil {
				app.searchButton.SetEnabled(true)
			}

			if err != nil {
				if strings.Contains(err.Error(), "context canceled") {
					app.onlineResultsLabel.SetText(fmt.Sprintf("Онлайн поиск по запросу '%s' отменен.", currentSearchTerm))
				} else {
					log.Printf("Ошибка онлайн поиска Jooble: %v", err)
					walk.MsgBox(app.MainWindow, "Ошибка поиска", fmt.Sprintf("Не удалось выполнить онлайн поиск: %v", err), walk.MsgBoxIconError)
					app.onlineResultsLabel.SetText(fmt.Sprintf("Ошибка онлайн поиска: %v", err))
				}
				return
			}

			filteredOnlineVacancies := []Vacancy{}
			allVacanciesMutex.Lock()
			for _, onlineV := range joobleVacancies {
				foundLocally := false
				select {
				case <-ch:
					allVacanciesMutex.Unlock()
					app.onlineResultsLabel.SetText(fmt.Sprintf("Онлайн поиск по запросу '%s' отменен в процессе фильтрации.", currentSearchTerm))
					return
				default:
				}
				for _, localV := range allVacancies {
					if strings.EqualFold(onlineV.Title, localV.Title) && strings.EqualFold(onlineV.Company, localV.Company) {
						foundLocally = true
						break
					}
				}
				if !foundLocally {
					filteredOnlineVacancies = append(filteredOnlineVacancies, onlineV)
				}
			}
			allVacanciesMutex.Unlock()

			app.onlineVacancyModel.items = filteredOnlineVacancies
			app.onlineVacancyModel.PublishRowsReset()
			if len(filteredOnlineVacancies) == 0 {
				select {
				case <-ch:
					app.onlineResultsLabel.SetText(fmt.Sprintf("Онлайн поиск по запросу '%s' отменен.", currentSearchTerm))
				default:
					if err != nil {
					} else {
						app.onlineResultsLabel.SetText(fmt.Sprintf("Онлайн поиск по запросу '%s' не дал новых результатов.", currentSearchTerm))
					}
				}
			} else {
				app.onlineResultsLabel.SetText(fmt.Sprintf("Найдено онлайн (новые): %d", len(filteredOnlineVacancies)))
			}
		})
	}(searchTerm, cancelChan)
}

// ДОБАВЛЕНО: Функция для открытия файла резюме
func (app *AppMainWindow) openResume() {
	idx := app.vacancyTable.CurrentIndex()
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		return
	}

	vacancy := app.vacancyModel.items[idx]
	if vacancy.ResumePath == "" {
		walk.MsgBox(app.MainWindow, "Информация", "Резюме не прикреплено к этой вакансии.", walk.MsgBoxIconInformation)
		return
	}

	cmd := exec.Command("cmd", "/c", "start", vacancy.ResumePath)
	err := cmd.Start()
	if err != nil {
		walk.MsgBox(app.MainWindow, "Ошибка", "Не удалось открыть файл резюме: "+err.Error(), walk.MsgBoxIconError)
	}
}

// ДОБАВЛЕНО: Функция для очистки прикрепленного резюме
func (app *AppMainWindow) clearResume() {
	idx := app.vacancyTable.CurrentIndex()
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		return
	}

	if walk.DlgCmdYes != walk.MsgBox(app.MainWindow, "Подтверждение",
		"Вы уверены, что хотите открепить файл резюме от этой вакансии?",
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) {
		return
	}

	originalIndex := app.findVacancyIndexInAllExt(app.vacancyModel.items[idx].Title, app.vacancyModel.items[idx].Company)
	if originalIndex != -1 {
		allVacancies[originalIndex].ResumePath = ""
		allVacancies[originalIndex].ResumeFileName = ""
		saveVacancies()
		app.updateVacancyDetails()
	}
}

// ДОБАВЛЕНО: Обработчик для drag-and-drop
func (app *AppMainWindow) handleFileDrop(files []string) {
	if len(files) == 0 {
		return
	}

	idx := app.vacancyTable.CurrentIndex()
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		walk.MsgBox(app.MainWindow, "Информация", "Пожалуйста, выберите вакансию для прикрепления резюме.", walk.MsgBoxIconInformation)
		return
	}

	// Берем только первый файл
	filePath := files[0]
	fileName := filepath.Base(filePath)

	// Проверяем расширение файла
	ext := strings.ToLower(filepath.Ext(fileName))
	allowedExts := map[string]bool{
		".pdf":  true,
		".doc":  true,
		".docx": true,
		".txt":  true,
		".rtf":  true,
	}

	if !allowedExts[ext] {
		walk.MsgBox(app.MainWindow, "Ошибка",
			"Неподдерживаемый формат файла. Разрешены только: PDF, DOC, DOCX, TXT, RTF",
			walk.MsgBoxIconError)
		return
	}

	originalIndex := app.findVacancyIndexInAllExt(app.vacancyModel.items[idx].Title, app.vacancyModel.items[idx].Company)
	if originalIndex != -1 {
		allVacancies[originalIndex].ResumePath = filePath
		allVacancies[originalIndex].ResumeFileName = fileName
		saveVacancies()
		app.updateVacancyDetails()
	}
}

// Добавляем новый метод для выбора файла резюме
func (app *AppMainWindow) selectResume() {
	idx := app.vacancyTable.CurrentIndex()
	if idx < 0 || idx >= len(app.vacancyModel.items) {
		walk.MsgBox(app.MainWindow, "Информация", "Пожалуйста, выберите вакансию для прикрепления резюме.", walk.MsgBoxIconInformation)
		return
	}

	dlg := new(walk.FileDialog)
	dlg.Title = "Выберите файл резюме"
	dlg.Filter = "Все поддерживаемые форматы (*.pdf;*.doc;*.docx;*.txt;*.rtf)|*.pdf;*.doc;*.docx;*.txt;*.rtf"

	if ok, err := dlg.ShowOpen(app.MainWindow); err != nil {
		walk.MsgBox(app.MainWindow, "Ошибка", "Ошибка при открытии диалога: "+err.Error(), walk.MsgBoxIconError)
	} else if ok {
		filePath := dlg.FilePath
		fileName := filepath.Base(filePath)
		ext := strings.ToLower(filepath.Ext(fileName))

		allowedExts := map[string]bool{
			".pdf":  true,
			".doc":  true,
			".docx": true,
			".txt":  true,
			".rtf":  true,
		}

		if !allowedExts[ext] {
			walk.MsgBox(app.MainWindow, "Ошибка",
				"Неподдерживаемый формат файла. Разрешены только: PDF, DOC, DOCX, TXT, RTF",
				walk.MsgBoxIconError)
			return
		}

		originalIndex := app.findVacancyIndexInAllExt(app.vacancyModel.items[idx].Title, app.vacancyModel.items[idx].Company)
		if originalIndex != -1 {
			allVacancies[originalIndex].ResumePath = filePath
			allVacancies[originalIndex].ResumeFileName = fileName
			saveVacancies()
			app.updateVacancyDetails()
		}
	}
}

// ДОБАВЛЕНО: Метод для применения темы
func (app *AppMainWindow) applyTheme(theme Theme) {
	currentTheme = theme

	// Основное окно и все контейнеры
	mainBrush, _ := walk.NewSolidColorBrush(theme.Background)
	defer mainBrush.Dispose()
	app.MainWindow.SetBackground(mainBrush)

	// Применяем тему к контейнерам
	containers := []*walk.Composite{
		app.localVacanciesContainer,
		app.onlineResultsContainer,
		app.detailResumeDropArea,
	}

	containerBrush, _ := walk.NewSolidColorBrush(theme.Background)
	defer containerBrush.Dispose()
	for _, container := range containers {
		if container != nil {
			container.SetBackground(containerBrush)
		}
	}

	// ScrollView отдельно
	if app.detailsScrollView != nil {
		scrollBrush, _ := walk.NewSolidColorBrush(theme.Background)
		defer scrollBrush.Dispose()
		app.detailsScrollView.SetBackground(scrollBrush)
	}

	// Группы (GroupBox)
	if app.detailsGroup != nil {
		groupBrush, _ := walk.NewSolidColorBrush(theme.PanelBG)
		defer groupBrush.Dispose()
		app.detailsGroup.SetBackground(groupBrush)
	}

	// Кнопки
	buttons := []*walk.PushButton{
		app.searchButton,
		app.addVacancyButton,
		app.editVacancyButton,
		app.deleteVacancyButton,
		app.onlineSearchButton,
		app.saveVacancyChangesPB,
		app.detailResumeOpenBtn,
		app.detailResumeClearBtn,
		app.themeToggleButton,
		app.resumeArchiveButton,
		app.backToLocalButton,
		app.cancelOnlineSearchButton,
	}

	buttonBrush, _ := walk.NewSolidColorBrush(theme.ButtonBG)
	defer buttonBrush.Dispose()
	for _, btn := range buttons {
		if btn != nil {
			btn.SetBackground(buttonBrush)
		}
	}

	// Таблицы
	tables := []*walk.TableView{
		app.vacancyTable,
		app.onlineResultsTable,
	}

	tableBrush, _ := walk.NewSolidColorBrush(theme.TableBG)
	defer tableBrush.Dispose()
	for _, table := range tables {
		if table != nil {
			table.SetBackground(tableBrush)
		}
	}

	// Метки
	labels := []*walk.Label{
		app.searchLabel,
		app.detailTitleLabel,
		app.detailTitleDisplay,
		app.detailCompanyLabel,
		app.detailCompanyDisplay,
		app.detailStatusLabel,
		app.detailExperienceLabel,
		app.detailKeywordsLabel,
		app.detailSourceURLLabel,
		app.detailDescriptionLabel,
		app.detailNotesLabel,
		app.detailResumeLabel,
		app.detailResumeDisplay,
		app.onlineResultsLabel,
	}

	for _, label := range labels {
		if label != nil {
			label.SetTextColor(theme.Text)
		}
	}

	// ComboBox'ы
	comboBoxes := []*walk.ComboBox{
		app.searchFieldCB,
		app.statusFilterCB,
		app.experienceFilterCB,
		app.detailStatusCB,
		app.detailExperienceCB,
	}

	comboBoxBrush, _ := walk.NewSolidColorBrush(theme.ButtonBG)
	defer comboBoxBrush.Dispose()
	for _, cb := range comboBoxes {
		if cb != nil {
			cb.SetBackground(comboBoxBrush)
		}
	}

	// LineEdit'ы
	lineEdits := []*walk.LineEdit{
		app.searchEdit,
		app.detailKeywordsLE,
		app.detailSourceURLLE,
	}

	editBrush, _ := walk.NewSolidColorBrush(theme.Background)
	defer editBrush.Dispose()
	for _, le := range lineEdits {
		if le != nil {
			le.SetBackground(editBrush)
			le.SetTextColor(theme.Text)
		}
	}

	// TextEdit'ы
	textEdits := []*walk.TextEdit{
		app.detailDescriptionTE,
		app.detailNotesTE,
	}

	textEditBrush, _ := walk.NewSolidColorBrush(theme.Background)
	defer textEditBrush.Dispose()
	for _, te := range textEdits {
		if te != nil {
			te.SetBackground(textEditBrush)
			te.SetTextColor(theme.Text)
		}
	}

	// Обновляем цвета статусов для тёмной темы
	if theme.Name == "Тёмная" {
		statusColors = map[string]walk.Color{
			"Новая": walk.RGB(0, 80, 0), // тёмно-зелёный
			"Планирую откликнуться": walk.RGB(80, 80, 0),  // тёмно-жёлтый
			"Откликнулся":           walk.RGB(0, 60, 80),  // тёмно-голубой
			"Тестовое задание":      walk.RGB(80, 60, 0),  // тёмно-оранжевый
			"Собеседование":         walk.RGB(60, 0, 80),  // тёмно-пурпурный
			"Оффер":                 walk.RGB(0, 100, 0),  // насыщенный зелёный
			"Отказ":                 walk.RGB(80, 0, 0),   // тёмно-красный
			"В архиве":              walk.RGB(50, 50, 50), // тёмно-серый
		}
	} else {
		statusColors = map[string]walk.Color{
			"Новая": walk.RGB(220, 255, 220), // светло-зелёный
			"Планирую откликнуться": walk.RGB(255, 255, 200), // светло-жёлтый
			"Откликнулся":           walk.RGB(210, 240, 255), // светло-голубой
			"Тестовое задание":      walk.RGB(255, 230, 200), // светло-оранжевый
			"Собеседование":         walk.RGB(240, 220, 255), // светло-пурпурный
			"Оффер":                 walk.RGB(180, 255, 180), // ярко-зелёный
			"Отказ":                 walk.RGB(255, 200, 200), // светло-красный
			"В архиве":              walk.RGB(220, 220, 220), // серый
		}
	}

	// Обновляем отображение таблицы для применения новых цветов статусов
	if app.vacancyTable != nil {
		app.vacancyTable.Invalidate()
	}
}

// ДОБАВЛЕНО: Метод для переключения темы
func (app *AppMainWindow) toggleTheme() {
	if currentTheme.Name == "Светлая" {
		app.applyTheme(darkTheme)
		app.themeToggleButton.SetText("☀ Светлая тема")
	} else {
		app.applyTheme(lightTheme)
		app.themeToggleButton.SetText("🌙 Тёмная тема")
	}
}

// ResumeArchiveEntry представляет запись в архиве резюме
type ResumeArchiveEntry struct {
	FileName    string
	FilePath    string
	VacancyName string
	Company     string
	AddedDate   string
}

// ResumeArchiveModel для TableView в окне архива
type ResumeArchiveModel struct {
	walk.TableModelBase
	items []ResumeArchiveEntry
}

func NewResumeArchiveModel() *ResumeArchiveModel {
	return &ResumeArchiveModel{items: []ResumeArchiveEntry{}}
}

func (m *ResumeArchiveModel) RowCount() int {
	return len(m.items)
}

func (m *ResumeArchiveModel) Value(row, col int) interface{} {
	item := m.items[row]
	switch col {
	case 0:
		return item.FileName
	case 1:
		return item.VacancyName
	case 2:
		return item.Company
	case 3:
		return item.AddedDate
	}
	return ""
}

// ResumeArchiveDialog представляет окно архива резюме
type ResumeArchiveDialog struct {
	*walk.Dialog
	model *ResumeArchiveModel
	table *walk.TableView
	owner *AppMainWindow
}

func ShowResumeArchive(owner *AppMainWindow) {
	dlg := &ResumeArchiveDialog{
		owner: owner,
		model: NewResumeArchiveModel(),
	}

	// Заполняем модель данными из всех вакансий
	for _, v := range allVacancies {
		if v.ResumeFileName != "" && v.ResumePath != "" {
			dlg.model.items = append(dlg.model.items, ResumeArchiveEntry{
				FileName:    v.ResumeFileName,
				FilePath:    v.ResumePath,
				VacancyName: v.Title,
				Company:     v.Company,
				AddedDate:   "", // В будущем можно добавить дату
			})
		}
	}

	if _, err := (Dialog{
		AssignTo:   &dlg.Dialog,
		Title:      "Архив резюме",
		MinSize:    Size{600, 400},
		Layout:     VBox{},
		Background: SolidColorBrush{Color: currentTheme.Background},
		Children: []Widget{
			TableView{
				AssignTo:   &dlg.table,
				Model:      dlg.model,
				Background: SolidColorBrush{Color: currentTheme.TableBG},
				Columns: []TableViewColumn{
					{Title: "Имя файла", Width: 150},
					{Title: "Вакансия", Width: 200},
					{Title: "Компания", Width: 150},
					{Title: "Дата добавления", Width: 100},
				},
				OnItemActivated: dlg.onItemActivated,
			},
			Composite{
				Layout:     HBox{},
				Background: SolidColorBrush{Color: currentTheme.Background},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text:       "Закрыть",
						Background: SolidColorBrush{Color: currentTheme.ButtonBG},
						OnClicked: func() {
							dlg.Accept()
						},
					},
				},
			},
		},
	}.Run(owner)); err != nil {
		log.Print("Dialog error: ", err)
	}
}

func (d *ResumeArchiveDialog) onItemActivated() {
	idx := d.table.CurrentIndex()
	if idx < 0 || idx >= len(d.model.items) {
		return
	}

	entry := d.model.items[idx]
	cmd := exec.Command("cmd", "/c", "start", entry.FilePath)
	if err := cmd.Start(); err != nil {
		walk.MsgBox(d.Dialog, "Ошибка", "Не удалось открыть файл резюме: "+err.Error(), walk.MsgBoxIconError)
	}
}

// showResumeArchive открывает окно архива резюме
func (app *AppMainWindow) showResumeArchive() {
	ShowResumeArchive(app)
}
