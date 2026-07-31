package metrics

import (
	"sort"
	"strings"
)

var standardCatalog = []Template{
	{Key: "revenue", Name: "Выручка", Description: "Объём дохода от основной деятельности до вычета расходов.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Сумма выручки по признанным продажам за период", Interpretation: "Показывает масштаб продаж, но сама по себе не говорит о прибыльности.", Aliases: []string{"sales", "оборот"}},
	{Key: "mrr", Name: "MRR", Description: "Регулярная месячная выручка от действующих подписок.", Category: "Финансы", Unit: "currency/month", ValueType: "currency", BetterDirection: "increase", Formula: "Сумма нормализованной месячной стоимости активных подписок", Interpretation: "Используется в подписочных моделях для оценки устойчивого темпа бизнеса.", Aliases: []string{"monthly recurring revenue"}},
	{Key: "arr", Name: "ARR", Description: "Регулярная годовая выручка от активных подписок.", Category: "Финансы", Unit: "currency/year", ValueType: "currency", BetterDirection: "increase", Formula: "MRR × 12", Interpretation: "Удобна для оценки масштаба подписочного бизнеса на годовом горизонте.", Aliases: []string{"annual recurring revenue"}},
	{Key: "gross_profit", Name: "Валовая прибыль", Description: "Выручка за вычетом прямой себестоимости.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка − себестоимость продаж", Interpretation: "Показывает, сколько остаётся для покрытия операционных расходов и прибыли.", Aliases: []string{"gross profit"}},
	{Key: "gross_margin", Name: "Валовая маржа", Description: "Доля валовой прибыли в выручке.", Category: "Финансы", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Валовая прибыль ÷ выручка × 100%", Interpretation: "Позволяет сравнивать экономику продуктов и периодов разного масштаба.", Aliases: []string{"gross margin"}},
	{Key: "contribution_margin", Name: "Маржинальная прибыль", Description: "Выручка за вычетом переменных расходов.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка − переменные расходы", Interpretation: "Показывает вклад продаж в покрытие постоянных расходов и прибыль.", Aliases: []string{"contribution margin"}},
	{Key: "contribution_margin_rate", Name: "Маржинальность", Description: "Доля маржинальной прибыли в выручке.", Category: "Финансы", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Маржинальная прибыль ÷ выручка × 100%", Interpretation: "Помогает оценивать качество роста и экономику направлений.", Aliases: []string{"contribution margin rate"}},
	{Key: "operating_profit", Name: "Операционная прибыль", Description: "Прибыль от основной деятельности до процентов и налогов.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Валовая прибыль − операционные расходы", Interpretation: "Показывает эффективность основной бизнес-модели.", Aliases: []string{"operating income", "EBIT", "снижение расходов", "оптимизация расходов", "cost reduction", "operating expenses"}},
	{Key: "ebitda", Name: "EBITDA", Description: "Прибыль до процентов, налогов, амортизации и износа.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Операционная прибыль + амортизация", Interpretation: "Приближённо показывает операционную доходность до структуры финансирования.", Aliases: []string{"ебитда", "снижение расходов", "оптимизация расходов", "cost reduction"}},
	{Key: "net_profit", Name: "Чистая прибыль", Description: "Финальный финансовый результат после всех расходов и налогов.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Доходы − все расходы − налоги", Interpretation: "Показывает итоговую прибыльность компании за период.", Aliases: []string{"net income"}},
	{Key: "operating_cash_flow", Name: "Операционный денежный поток", Description: "Денежный поток от основной деятельности.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Денежные поступления от операций − операционные выплаты", Interpretation: "Показывает способность бизнеса самостоятельно генерировать деньги.", Aliases: []string{"OCF", "cash flow", "контроль расходов", "снижение расходов", "cash control"}},
	{Key: "burn_rate", Name: "Burn rate", Description: "Средний объём денежных средств, который компания расходует сверх поступлений.", Category: "Финансы", Unit: "currency/month", ValueType: "currency", BetterDirection: "decrease", Formula: "Уменьшение денежных средств за период ÷ число месяцев", Interpretation: "Используется для контроля скорости расходования денежного запаса.", Aliases: []string{"темп сжигания денег", "снижение расходов", "оптимизация расходов", "экономия", "cost reduction"}},
	{Key: "runway", Name: "Финансовый runway", Description: "Количество месяцев до исчерпания денежных средств при текущем burn rate.", Category: "Финансы", Unit: "months", ValueType: "duration", BetterDirection: "increase", Formula: "Доступные денежные средства ÷ месячный burn rate", Interpretation: "Показывает время на достижение безубыточности или привлечение финансирования.", Aliases: []string{"cash runway"}},
	{Key: "working_capital", Name: "Оборотный капитал", Description: "Ресурс для финансирования текущей операционной деятельности.", Category: "Финансы", Unit: "currency", ValueType: "currency", BetterDirection: "range", Formula: "Оборотные активы − краткосрочные обязательства", Interpretation: "Слишком низкое значение создаёт кассовые риски, слишком высокое может говорить о замороженных средствах.", Aliases: []string{"working capital"}},
	{Key: "roic", Name: "ROIC", Description: "Доходность инвестированного в бизнес капитала.", Category: "Финансы", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Операционная прибыль после налогов ÷ инвестированный капитал × 100%", Interpretation: "Показывает, насколько эффективно бизнес превращает капитал в прибыль.", Aliases: []string{"return on invested capital"}},

	{Key: "sales_pipeline", Name: "Объём воронки продаж", Description: "Потенциальная стоимость активных сделок.", Category: "Продажи", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Сумма потенциальной стоимости всех активных сделок", Interpretation: "Показывает будущий коммерческий потенциал, но требует учёта вероятностей.", Aliases: []string{"pipeline"}},
	{Key: "qualified_pipeline", Name: "Квалифицированная воронка", Description: "Стоимость сделок, прошедших критерии квалификации.", Category: "Продажи", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Сумма стоимости квалифицированных активных сделок", Interpretation: "Лучше отражает реальный потенциал продаж, чем общий объём лидов.", Aliases: []string{"qualified pipeline"}},
	{Key: "lead_to_sale_conversion", Name: "Конверсия лида в продажу", Description: "Доля лидов, ставших клиентами.", Category: "Продажи", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Количество новых клиентов ÷ количество лидов × 100%", Interpretation: "Показывает качество лидов и эффективность процесса продаж.", Aliases: []string{"sales conversion"}},
	{Key: "win_rate", Name: "Win rate", Description: "Доля выигранных сделок среди завершённых.", Category: "Продажи", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Выигранные сделки ÷ все завершённые сделки × 100%", Interpretation: "Отражает конкурентоспособность предложения и качество продаж.", Aliases: []string{"процент побед"}},
	{Key: "average_deal_size", Name: "Средний размер сделки", Description: "Средняя выручка по одной закрытой сделке.", Category: "Продажи", Unit: "currency", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка по новым сделкам ÷ количество выигранных сделок", Interpretation: "Помогает оценивать сегмент, пакетирование и экономику отдела продаж.", Aliases: []string{"average contract value", "ACV"}},
	{Key: "sales_cycle", Name: "Длительность цикла продажи", Description: "Среднее время от квалификации лида до закрытия сделки.", Category: "Продажи", Unit: "days", ValueType: "duration", BetterDirection: "decrease", Formula: "Сумма длительностей выигранных сделок ÷ количество сделок", Interpretation: "Влияет на скорость роста и потребность в оборотном капитале.", Aliases: []string{"sales cycle"}},
	{Key: "quota_attainment", Name: "Выполнение плана продаж", Description: "Степень выполнения коммерческого плана.", Category: "Продажи", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Фактические продажи ÷ план продаж × 100%", Interpretation: "Показывает достижимость плана и эффективность коммерческой команды.", Aliases: []string{"quota attainment"}},
	{Key: "revenue_per_salesperson", Name: "Выручка на продавца", Description: "Средний объём выручки на одного сотрудника продаж.", Category: "Продажи", Unit: "currency/person", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка ÷ среднее количество продавцов", Interpretation: "Используется для оценки производительности и масштабируемости продаж.", Aliases: []string{"sales productivity"}},

	{Key: "cac", Name: "CAC", Description: "Средняя стоимость привлечения одного нового клиента.", Category: "Маркетинг", Unit: "currency/customer", ValueType: "currency", BetterDirection: "decrease", Formula: "Расходы на маркетинг и продажи ÷ количество новых клиентов", Interpretation: "Сравнивается с LTV и маржой клиента.", Aliases: []string{"customer acquisition cost"}},
	{Key: "cpl", Name: "CPL", Description: "Средняя стоимость получения одного лида.", Category: "Маркетинг", Unit: "currency/lead", ValueType: "currency", BetterDirection: "decrease", Formula: "Маркетинговые расходы ÷ количество полученных лидов", Interpretation: "Показывает эффективность закупки лидов, но не их качество.", Aliases: []string{"cost per lead"}},
	{Key: "mql_to_sql", Name: "Конверсия MQL в SQL", Description: "Доля маркетинговых лидов, принятых продажами.", Category: "Маркетинг", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "SQL ÷ MQL × 100%", Interpretation: "Показывает качество маркетинговой квалификации и согласованность команд.", Aliases: []string{"MQL conversion"}},
	{Key: "roas", Name: "ROAS", Description: "Возврат рекламных расходов в виде выручки.", Category: "Маркетинг", Unit: "ratio", ValueType: "ratio", BetterDirection: "increase", Formula: "Выручка от рекламы ÷ рекламные расходы", Interpretation: "Не учитывает прочие расходы и маржинальность.", Aliases: []string{"return on ad spend"}},
	{Key: "romi", Name: "ROMI", Description: "Доходность маркетинговых инвестиций.", Category: "Маркетинг", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "(Маржинальная прибыль от маркетинга − затраты) ÷ затраты × 100%", Interpretation: "Лучше ROAS учитывает экономический результат, если корректно рассчитана маржа.", Aliases: []string{"marketing ROI"}},
	{Key: "website_conversion", Name: "Конверсия сайта", Description: "Доля посетителей, совершивших целевое действие.", Category: "Маркетинг", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Целевые действия ÷ уникальные посетители × 100%", Interpretation: "Требует чёткого определения целевого действия и сегмента трафика.", Aliases: []string{"site conversion"}},
	{Key: "organic_traffic", Name: "Органический трафик", Description: "Количество визитов из неоплачиваемых поисковых каналов.", Category: "Маркетинг", Unit: "visits", ValueType: "number", BetterDirection: "increase", Formula: "Количество органических визитов за период", Interpretation: "Рост полезен только вместе с качеством и конверсией трафика.", Aliases: []string{"SEO traffic"}},

	{Key: "activation_rate", Name: "Активация пользователей", Description: "Доля новых пользователей, достигших первого ценного результата.", Category: "Продукт", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Активированные новые пользователи ÷ все новые пользователи × 100%", Interpretation: "Событие активации должно отражать полученную ценность, а не простой вход.", Aliases: []string{"activation rate"}},
	{Key: "time_to_value", Name: "Time to value", Description: "Время до получения пользователем первого значимого результата.", Category: "Продукт", Unit: "days", ValueType: "duration", BetterDirection: "decrease", Formula: "Среднее время от начала использования до события ценности", Interpretation: "Чем меньше путь до ценности, тем выше вероятность активации и удержания.", Aliases: []string{"TTV"}},
	{Key: "mau", Name: "MAU", Description: "Количество уникальных активных пользователей за месяц.", Category: "Продукт", Unit: "users", ValueType: "number", BetterDirection: "increase", Formula: "Уникальные пользователи с целевой активностью за 30 дней", Interpretation: "Определение активности должно отражать использование ценности продукта.", Aliases: []string{"monthly active users"}},
	{Key: "dau_mau", Name: "DAU/MAU", Description: "Доля месячной аудитории, активной в средний день.", Category: "Продукт", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "DAU ÷ MAU × 100%", Interpretation: "Отражает частоту использования, но желаемое значение зависит от продукта.", Aliases: []string{"stickiness"}},
	{Key: "retention_d30", Name: "Retention D30", Description: "Доля пользователей, вернувшихся или оставшихся активными на 30-й день.", Category: "Продукт", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Активные на 30-й день пользователи когорты ÷ размер когорты × 100%", Interpretation: "Когортное измерение помогает отличить удержание от роста новых регистраций.", Aliases: []string{"30 day retention"}},
	{Key: "customer_churn", Name: "Отток клиентов", Description: "Доля клиентов, потерянных за период.", Category: "Продукт", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Ушедшие клиенты ÷ клиенты на начало периода × 100%", Interpretation: "Нужно отдельно учитывать добровольный и недобровольный отток.", Aliases: []string{"customer churn"}},
	{Key: "revenue_churn", Name: "Отток выручки", Description: "Доля регулярной выручки, потерянной из-за сокращений и ухода клиентов.", Category: "Продукт", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Потерянная регулярная выручка ÷ выручка на начало периода × 100%", Interpretation: "Показывает финансовый эффект оттока с учётом размера клиентов.", Aliases: []string{"MRR churn"}},
	{Key: "feature_adoption", Name: "Использование функции", Description: "Доля целевых пользователей, использующих конкретную функцию.", Category: "Продукт", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Пользователи функции ÷ целевые активные пользователи × 100%", Interpretation: "Помогает оценить реальную ценность конкретного изменения продукта.", Aliases: []string{"feature adoption"}},

	{Key: "ltv", Name: "LTV", Description: "Ожидаемая маржинальная ценность клиента за весь срок отношений.", Category: "Клиенты", Unit: "currency/customer", ValueType: "currency", BetterDirection: "increase", Formula: "Средняя маржинальная прибыль за период × средний срок жизни клиента", Interpretation: "Методика должна учитывать маржу и быть стабильной между периодами.", Aliases: []string{"customer lifetime value"}},
	{Key: "ltv_cac", Name: "LTV/CAC", Description: "Соотношение ценности клиента и стоимости его привлечения.", Category: "Клиенты", Unit: "ratio", ValueType: "ratio", BetterDirection: "range", Formula: "LTV ÷ CAC", Interpretation: "Слишком низкое значение означает слабую экономику, слишком высокое может говорить о недоинвестировании в рост.", Aliases: []string{"LTV to CAC"}},
	{Key: "arpu", Name: "ARPU", Description: "Средняя выручка на одного активного пользователя или клиента.", Category: "Клиенты", Unit: "currency/customer", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка ÷ среднее количество активных клиентов", Interpretation: "Полезна для оценки монетизации и структуры клиентской базы.", Aliases: []string{"average revenue per user"}},
	{Key: "aov", Name: "Средний чек", Description: "Средняя сумма одного заказа.", Category: "Клиенты", Unit: "currency/order", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка ÷ количество заказов", Interpretation: "Рост среднего чека следует оценивать вместе с конверсией и повторными покупками.", Aliases: []string{"average order value"}},
	{Key: "repeat_purchase_rate", Name: "Доля повторных покупок", Description: "Доля клиентов, совершивших повторную покупку.", Category: "Клиенты", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Клиенты с повторной покупкой ÷ все покупатели × 100%", Interpretation: "Показывает способность продукта удерживать спрос после первой покупки.", Aliases: []string{"repeat rate"}},
	{Key: "repeat_revenue_share", Name: "Доля повторной выручки", Description: "Доля выручки от клиентов, покупавших ранее.", Category: "Клиенты", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Выручка от повторных клиентов ÷ общая выручка × 100%", Interpretation: "Отражает устойчивость выручки и качество клиентской базы.", Aliases: []string{"repeat revenue"}},
	{Key: "nps", Name: "NPS", Description: "Готовность клиентов рекомендовать компанию.", Category: "Клиенты", Unit: "points", ValueType: "number", BetterDirection: "increase", Formula: "Доля промоутеров − доля критиков", Interpretation: "Диапазон от −100 до 100; важна стабильная методика опроса.", Aliases: []string{"net promoter score"}},
	{Key: "csat", Name: "CSAT", Description: "Удовлетворённость клиентов конкретным опытом или взаимодействием.", Category: "Клиенты", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Положительные оценки ÷ все ответы × 100%", Interpretation: "Лучше измерять сразу после конкретного события.", Aliases: []string{"customer satisfaction"}},

	{Key: "on_time_delivery", Name: "Доставка вовремя", Description: "Доля заказов, доставленных в обещанный срок.", Category: "Операции", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Заказы в срок ÷ все доставленные заказы × 100%", Interpretation: "Показывает надёжность исполнения клиентского обещания.", Aliases: []string{"OTD"}},
	{Key: "fulfillment_time", Name: "Срок исполнения заказа", Description: "Время от подтверждения заказа до готовности или отправки.", Category: "Операции", Unit: "hours", ValueType: "duration", BetterDirection: "decrease", Formula: "Среднее время исполнения завершённых заказов", Interpretation: "Нужно отдельно смотреть медиану и выбросы при нестабильном процессе.", Aliases: []string{"order fulfillment time"}},
	{Key: "cost_per_order", Name: "Операционная стоимость заказа", Description: "Средние операционные расходы на обработку одного заказа.", Category: "Операции", Unit: "currency/order", ValueType: "currency", BetterDirection: "decrease", Formula: "Операционные расходы на исполнение ÷ количество заказов", Interpretation: "Используется для контроля масштабируемости операций.", Aliases: []string{"cost per order"}},
	{Key: "defect_rate", Name: "Уровень дефектов", Description: "Доля единиц продукта или операций с дефектом.", Category: "Операции", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Количество дефектов ÷ проверенное количество × 100%", Interpretation: "Определение дефекта и контрольная выборка должны быть стабильными.", Aliases: []string{"defect rate"}},
	{Key: "return_rate", Name: "Доля возвратов", Description: "Доля заказов или единиц товара, возвращённых клиентами.", Category: "Операции", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Возвращённые заказы ÷ доставленные заказы × 100%", Interpretation: "Причины возвратов нужно анализировать отдельно: качество, ожидания, логистика.", Aliases: []string{"return rate"}},
	{Key: "order_accuracy", Name: "Точность комплектации", Description: "Доля заказов без ошибок в составе и количестве.", Category: "Операции", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Заказы без ошибок ÷ все собранные заказы × 100%", Interpretation: "Отражает качество складского и производственного процесса.", Aliases: []string{"order accuracy"}},
	{Key: "capacity_utilization", Name: "Загрузка мощностей", Description: "Доля доступной производственной мощности, которая фактически используется.", Category: "Операции", Unit: "%", ValueType: "percent", BetterDirection: "range", Formula: "Фактический выпуск ÷ доступная мощность × 100%", Interpretation: "Слишком низкая загрузка неэффективна, слишком высокая создаёт риски качества и сроков.", Aliases: []string{"capacity utilization"}},
	{Key: "inventory_days", Name: "Дни запаса", Description: "На сколько дней продаж хватит текущего товарного запаса.", Category: "Операции", Unit: "days", ValueType: "duration", BetterDirection: "range", Formula: "Средний запас ÷ себестоимость продаж × количество дней периода", Interpretation: "Оптимум зависит от поставок, сезонности и уровня сервиса.", Aliases: []string{"days inventory outstanding", "DIO"}},
	{Key: "inventory_turnover", Name: "Оборачиваемость запасов", Description: "Сколько раз запас продаётся и заменяется за период.", Category: "Операции", Unit: "ratio", ValueType: "ratio", BetterDirection: "increase", Formula: "Себестоимость продаж ÷ средний запас", Interpretation: "Высокая оборачиваемость полезна, пока не создаёт дефицит.", Aliases: []string{"inventory turnover"}},
	{Key: "first_response_time", Name: "Время первого ответа", Description: "Время от обращения клиента до первого содержательного ответа.", Category: "Операции", Unit: "minutes", ValueType: "duration", BetterDirection: "decrease", Formula: "Среднее время первого ответа по обращениям", Interpretation: "Оценивает доступность поддержки, но не качество решения.", Aliases: []string{"FRT"}},
	{Key: "resolution_time", Name: "Время решения обращения", Description: "Время от открытия обращения до его фактического решения.", Category: "Операции", Unit: "hours", ValueType: "duration", BetterDirection: "decrease", Formula: "Средняя длительность закрытых обращений", Interpretation: "Следует сегментировать по типу и сложности обращений.", Aliases: []string{"time to resolution"}},

	{Key: "revenue_per_employee", Name: "Выручка на сотрудника", Description: "Средний объём выручки на одного сотрудника.", Category: "Команда", Unit: "currency/person", ValueType: "currency", BetterDirection: "increase", Formula: "Выручка ÷ средняя численность команды", Interpretation: "Показывает общую производительность, но зависит от бизнес-модели и аутсорсинга.", Aliases: []string{"revenue per FTE"}},
	{Key: "employee_turnover", Name: "Текучесть сотрудников", Description: "Доля сотрудников, покинувших компанию за период.", Category: "Команда", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Ушедшие сотрудники ÷ средняя численность × 100%", Interpretation: "Важно отдельно смотреть добровольные и управляемые увольнения.", Aliases: []string{"employee churn"}},
	{Key: "time_to_hire", Name: "Срок закрытия вакансии", Description: "Время от открытия вакансии до принятия оффера.", Category: "Команда", Unit: "days", ValueType: "duration", BetterDirection: "decrease", Formula: "Среднее число дней по закрытым вакансиям", Interpretation: "Нужно оценивать вместе с качеством найма.", Aliases: []string{"time to hire"}},
	{Key: "team_utilization", Name: "Утилизация команды", Description: "Доля доступного рабочего времени, занятого целевой работой.", Category: "Команда", Unit: "%", ValueType: "percent", BetterDirection: "range", Formula: "Целевые продуктивные часы ÷ доступные часы × 100%", Interpretation: "Максимальная загрузка не всегда полезна: она снижает устойчивость и скорость реакции.", Aliases: []string{"utilization"}},
	{Key: "enps", Name: "eNPS", Description: "Готовность сотрудников рекомендовать компанию как место работы.", Category: "Команда", Unit: "points", ValueType: "number", BetterDirection: "increase", Formula: "Доля промоутеров − доля критиков", Interpretation: "Сигнал вовлечённости, который нужно дополнять качественной обратной связью.", Aliases: []string{"employee NPS"}},

	{Key: "ecommerce_conversion", Name: "Конверсия интернет-магазина", Description: "Доля сессий, завершившихся покупкой.", Category: "E-commerce", Unit: "%", ValueType: "percent", BetterDirection: "increase", Formula: "Количество заказов ÷ количество сессий × 100%", Interpretation: "Сравнивать нужно внутри сопоставимых каналов, устройств и рынков.", Aliases: []string{"purchase conversion"}},
	{Key: "cart_abandonment", Name: "Брошенные корзины", Description: "Доля созданных корзин, не завершившихся заказом.", Category: "E-commerce", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Незавершённые корзины ÷ все созданные корзины × 100%", Interpretation: "Помогает находить трение в оформлении, оплате и доставке.", Aliases: []string{"cart abandonment"}},
	{Key: "refund_rate", Name: "Доля возврата денег", Description: "Доля оплаченной выручки, возвращённой клиентам.", Category: "E-commerce", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Сумма возвратов денег ÷ оплаченная выручка × 100%", Interpretation: "Финансово точнее количества возвратов при разном размере заказов.", Aliases: []string{"refund rate"}},
	{Key: "stockout_rate", Name: "Доля отсутствия товара", Description: "Доля спроса или SKU, недоступных из-за отсутствия запаса.", Category: "E-commerce", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Недоступные позиции или случаи спроса ÷ общее количество × 100%", Interpretation: "Показывает потерянные продажи и качество планирования запасов.", Aliases: []string{"out of stock rate"}},
	{Key: "shipping_cost_share", Name: "Доля логистики в выручке", Description: "Доля расходов на доставку и исполнение в выручке.", Category: "E-commerce", Unit: "%", ValueType: "percent", BetterDirection: "decrease", Formula: "Расходы на логистику ÷ выручка × 100%", Interpretation: "Помогает контролировать экономику международной и региональной доставки.", Aliases: []string{"shipping cost ratio"}},
}

func Catalog(query string, category string) []Template {
	query = strings.ToLower(strings.TrimSpace(query))
	category = strings.TrimSpace(category)
	type scoredTemplate struct {
		item  Template
		score int
	}
	scored := make([]scoredTemplate, 0, len(standardCatalog))
	hasExactMatch := false
	for _, item := range standardCatalog {
		if category != "" && item.Category != category {
			continue
		}
		score := catalogMatchScore(item, query)
		if query != "" && score == 0 {
			continue
		}
		scored = append(scored, scoredTemplate{item: item, score: score})
		if score >= 100 {
			hasExactMatch = true
		}
	}
	if hasExactMatch {
		exact := scored[:0]
		for _, item := range scored {
			if item.score >= 100 {
				exact = append(exact, item)
			}
		}
		scored = exact
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].item.Category == scored[j].item.Category {
			return scored[i].item.Name < scored[j].item.Name
		}
		return scored[i].item.Category < scored[j].item.Category
	})
	result := make([]Template, 0, len(scored))
	for _, item := range scored {
		result = append(result, item.item)
	}
	return result
}

func TemplateByKey(key string) (Template, bool) {
	for _, item := range standardCatalog {
		if item.Key == strings.TrimSpace(key) {
			return item, true
		}
	}
	return Template{}, false
}

func Categories() []string {
	seen := map[string]bool{}
	result := []string{}
	for _, item := range standardCatalog {
		if !seen[item.Category] {
			seen[item.Category] = true
			result = append(result, item.Category)
		}
	}
	sort.Strings(result)
	return result
}

func catalogMatch(item Template, query string) bool {
	return catalogMatchScore(item, strings.ToLower(strings.TrimSpace(query))) > 0
}

func catalogMatchScore(item Template, query string) int {
	if query == "" {
		return 1
	}
	values := []string{item.Key, item.Name, item.Description, item.Formula}
	values = append(values, item.Aliases...)
	score := 0
	for _, value := range values {
		value = strings.ToLower(value)
		if strings.Contains(value, query) {
			score += 100
		}
		for _, term := range catalogSearchTerms(query) {
			if strings.Contains(value, term) {
				score += 10
			}
		}
	}
	return score
}

func catalogSearchTerms(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'а' || r > 'я') && r != 'ё'
	})
	seen := map[string]bool{}
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		runes := []rune(field)
		if len(runes) < 4 {
			continue
		}
		if len(runes) > 6 {
			runes = runes[:len(runes)-2]
		}
		term := string(runes)
		if !seen[term] {
			seen[term] = true
			result = append(result, term)
		}
	}
	return result
}
